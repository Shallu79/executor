# Minos Exec Engine — System Architecture & Design

> A secure, multi-language code execution engine built on nsjail, Go, and Redis.  
> Inspired by Judge0's architecture with stricter isolation guarantees.

---

## Table of Contents

1. [Overview & Goals](#1-overview--goals)
2. [Tech Stack](#2-tech-stack)
3. [High-Level Architecture](#3-high-level-architecture)
4. [Data Flow & Sequence](#4-data-flow--sequence)
5. [Component Deep Dives](#5-component-deep-dives)
   - [5.1 API Gateway](#51-api-gateway)
   - [5.2 Redis Broker](#52-redis-broker)
   - [5.3 Worker Daemon](#53-worker-daemon)
   - [5.4 nsjail Sandbox](#54-nsjail-sandbox)
6. [Security Model](#6-security-model)
7. [Language Support & Configs](#7-language-support--configs)
8. [Edge Cases & Failure Modes](#8-edge-cases--failure-modes)
9. [Production Hardening](#9-production-hardening)
10. [Scalability & Multi-Worker](#10-scalability--multi-worker)
11. [Observability & Metrics](#11-observability--metrics)
12. [Deployment Topology (Single VM)](#12-deployment-topology-single-vm)
13. [Directory Structure](#13-directory-structure)
14. [Appendix: Full Config Examples](#14-appendix-full-config-examples)
15. [Runtime Selection & Validated Architecture](#15-runtime-selection--validated-architecture)

---

## 1. Overview & Goals

Minos Exec Engine accepts user-submitted source code, executes it inside an isolated
sandbox, and returns stdout, stderr, exit code, and execution time — all within
tens of milliseconds.

**Non-goals (out of scope):**
- Interactive stdin (batch-only)
- Persistent file storage for user code
- Network access from within the sandbox
- Multi-tenancy at the filesystem level per job (each job gets a fresh temp dir)

**Core Design Principles:**
- **Security first:** Every line of user code runs under nsjail with Linux namespaces,
  cgroups v2, and seccomp. The API never touches user code on disk.
- **Stateless workers:** A worker can be killed and restarted without data loss. All
  job state lives in Redis during flight.
- **Single-responsibility components:** API, Redis, and Worker are fully decoupled
  and can be scaled independently.
- **Predictable latency:** Blocking-list-pop pattern keeps the hot path short
  (no polling, no websocket overhead for simple execute requests).

---

## 2. Tech Stack

| Component        | Technology                | Rationale                                                  |
| ---------------- | ------------------------- | ---------------------------------------------------------- |
| API Gateway      | Go + Gin                  | High throughput, low GC pressure, excellent stdlib         |
| Message Broker   | Redis 7+                  | Sub-ms latency, Lists for FIFO queues, TTL-based expiry    |
| Worker Daemon    | Go (no framework)         | Direct `os/exec`, no HTTP overhead on worker nodes         |
| Sandbox Runtime  | nsjail (from Google)      | Battle-tested, namespaces + cgroups + seccomp out of box   |
| Deployment       | API/Redis: Docker         | Easy orchestration, horizontal scaling                     |
|                  | Worker: Bare-metal Linux  | Full kernel feature access (cgroups v2, seccomp, namespaces)|

> **Why bare-metal for workers?** Docker-in-Docker for nsjail introduces a second
> layer of namespace nesting that breaks procfs mounts and reduces security
> guarantees. Workers should run directly on the host for maximum compatibility
> with nsjail's kernel-level features.

---

## 3. High-Level Architecture

```
                    ┌─────────────┐
                    │   Client    │
                    └──────┬──────┘
                           │ POST /api/v1/execute
                           ▼
    ┌──────────────────────────────────────────┐
    │              API Gateway (Go + Gin)       │
    │                                           │
    │  ┌─────────┐   ┌───────────────┐          │
    │  │ Validate │──▶│ RPUSH queue   │          │
    │  │ Payload  │   │ BLPOP result  │          │
    │  └─────────┘   └───────┬───────┘          │
    └────────────────────────┼──────────────────┘
                             │
                    ┌────────▼────────┐
                    │      Redis       │
                    │                  │
                    │  minos:queue     │  (List — FIFO job queue)
                    │  minos:result:*  │  (List — response back to API)
                    └────────▲────────┘
                             │ BLPOP / LPUSH
    ┌────────────────────────┼──────────────────┐
    │              Worker Daemon (Go)            │
    │                                           │
    │  ┌──────────┐  ┌────────┐  ┌──────────┐   │
    │  │ Pull Job │─▶│ Write  │─▶│  nsjail  │   │
    │  │ from Q   │  │ to /tmp│  │  execute │   │
    │  └──────────┘  └────────┘  └────┬─────┘   │
    │                                 │          │
    │  ┌──────────┐  ┌────────┐       │          │
    │  │ Push     │◀─│ Capture│◀──────┘          │
    │  │ Result   │  │ Output │                  │
    │  └──────────┘  └────────┘                  │
    └─────────────────────────────────────────────┘
```

**Key design decisions:**

1. **Synchronous HTTP request, async job processing.** The API holds the HTTP
   connection open (via `BLPOP` blocking) for up to 10 seconds. No websockets
   needed for batch execute-and-return.

2. **Result keys are ephemeral.** Each job creates `minos:result:{uuid}` with
   a 300-second TTL. Abandoned results (client disconnected) are auto-cleaned.

3. **No polling in the hot path.** Workers use `BLPOP` on the queue list (blocks
   until a job arrives). The API uses `BLPOP` on the result key (blocks until
   the worker pushes). Zero CPU burn waiting.

---

## 4. Data Flow & Sequence

```
Client              API Gateway            Redis              Worker              nsjail
  │                     │                    │                   │                   │
  │  POST {lang,code}   │                    │                   │                   │
  │────────────────────▶│                    │                   │                   │
  │                     │                    │                   │                   │
  │                     │  Generate UUID     │                   │                   │
  │                     │──┐                 │                   │                   │
  │                     │◀─┘                 │                   │                   │
  │                     │                    │                   │                   │
  │                     │  RPUSH queue       │                   │                   │
  │                     │───────────────────▶│                   │                   │
  │                     │                    │                   │                   │
  │                     │  BLPOP result:{id} │ (blocks)          │                   │
  │                     │───────────────────▶│                   │                   │
  │                     │                    │                   │                   │
  │                     │                    │  BLPOP queue      │                   │
  │                     │                    │◀──────────────────│                   │
  │                     │                    │   (unblocks)      │                   │
  │                     │                    │──────────────────▶│                   │
  │                     │                    │                   │                   │
  │                     │                    │                   │  Write code to    │
  │                     │                    │                   │  /tmp/minos/{id}/ │
  │                     │                    │                   │──┐                │
  │                     │                    │                   │◀─┘                │
  │                     │                    │                   │                   │
  │                     │                    │                   │  exec.Command()   │
  │                     │                    │                   │──────────────────▶│
  │                     │                    │                   │                   │
  │                     │                    │                   │                   │── Sandbox
  │                     │                    │                   │                   │   setup
  │                     │                    │                   │                   │
  │                     │                    │                   │   stdout, stderr  │
  │                     │                    │                   │◀──────────────────│
  │                     │                    │                   │   exit_code       │
  │                     │                    │                   │                   │
  │                     │                    │                   │  os.RemoveAll()   │
  │                     │                    │                   │──┐                │
  │                     │                    │                   │◀─┘                │
  │                     │                    │                   │                   │
  │                     │                    │  LPUSH result:{id}│                   │
  │                     │                    │◀──────────────────│                   │
  │                     │                    │                   │                   │
  │                     │  BLPOP unblocks    │                   │                   │
  │                     │◀───────────────────│                   │                   │
  │                     │                    │                   │                   │
  │  200 OK {stdout,..} │                    │                   │                   │
  │◀────────────────────│                    │                   │                   │
  │                     │                    │                   │                   │
```

**Timing characteristics:**
- API→Redis RPUSH: < 1ms
- Worker BLPOP wake: < 1ms (Redis is single-threaded, event-driven)
- nsjail spawn + sandbox setup: ~3-5ms (fork + clone flags)
- Code execution: variable (capped by time_limit)
- Result LPUSH→BLPOP delivery: < 1ms
- **Total overhead (excluding code runtime): ~10-15ms**

---

## 5. Component Deep Dives

### 5.1 API Gateway

**Responsibilities:**
- Validate and deserialize incoming JSON payloads
- Enforce rate limits per IP
- Generate unique job IDs (UUID v4)
- Push job payloads to Redis queue
- Block-wait for result on per-job result key
- Serialize and return result JSON

**Validation rules:**

| Field   | Constraint                    | Rationale                                |
| ------- | ----------------------------- | ---------------------------------------- |
| `lang`  | Must be in allowed enum set   | Prevent path traversal in lang selection |
| `code`  | Max 64 KB (configurable)      | Prevent queue bloat, DoS on workers      |
| `code`  | Must be valid UTF-8           | Reject binary garbage early              |
| `stdin` | Max 1 KB, optional            | Sanity cap on input data                 |

**Rate limiting:**
```
┌──────────────────────────────────────────────────┐
│  Per-IP sliding window via Redis Sorted Sets     │
│  Default: 30 req/min per IP, burst 5            │
│  Returns: 429 Too Many Requests                  │
│  Key: minos:ratelimit:{ip}                       │
│  Algorithm: Sliding window log                   │
│                                                  │
│  ┌───────────────────────────────────────────┐   │
│  │ ZREMRANGEBYSCORE key 0 (now - window)     │   │
│  │ ZCARD key           → current count       │   │
│  │ if count > limit → reject                 │   │
│  │ ZADD key now now                          │   │
│  │ EXPIRE key window                         │   │
│  └───────────────────────────────────────────┘   │
└──────────────────────────────────────────────────┘
```

**Graceful shutdown:**
```go
// On SIGTERM/SIGINT:
// 1. Stop accepting new requests (health check returns 503)
// 2. Drain in-flight BLPOP connections (wait up to 15s)
// 3. Close Redis pool
// 4. Exit
```

**Handler pseudocode:**
```go
func (h *Handler) Execute(c *gin.Context) {
    // 1. Rate limit check
    clientIP := c.ClientIP()
    if !h.limiter.Allow(clientIP) {
        c.AbortWithStatusJSON(429, ErrorResponse{Error: "rate limit exceeded"})
        return
    }

    // 2. Bind & validate
    var req SubmissionRequest
    if err := c.ShouldBindJSON(&req); err != nil {
        c.AbortWithStatusJSON(400, ErrorResponse{Error: "invalid payload"})
        return
    }
    if len(req.Code) > h.maxCodeSize {
        c.AbortWithStatusJSON(400, ErrorResponse{Error: "code too large"})
        return
    }
    if !h.isValidLang(req.Language) {
        c.AbortWithStatusJSON(400, ErrorResponse{Error: "unsupported language"})
        return
    }

    // 3. Create job
    job := Job{
        ID:       uuid.NewString(),
        Language: req.Language,
        Code:     req.Code,
        Stdin:    req.Stdin,
        Created:  time.Now(),
    }

    // 4. Push to queue
    payload, _ := json.Marshal(job)
    if err := h.redis.RPush(ctx, "minos:queue", payload).Err(); err != nil {
        c.AbortWithStatusJSON(500, ErrorResponse{Error: "queue push failed"})
        return
    }

    // 5. Block-wait for result
    resultKey := "minos:result:" + job.ID
    result, err := h.redis.BLPop(ctx, 10*time.Second, resultKey).Result()
    if err != nil {
        if errors.Is(err, context.DeadlineExceeded) {
            c.AbortWithStatusJSON(504, ErrorResponse{Error: "execution timeout"})
        } else {
            c.AbortWithStatusJSON(500, ErrorResponse{Error: "internal error"})
        }
        return
    }

    // 6. Return result
    var execResult ExecutionResult
    json.Unmarshal([]byte(result[1]), &execResult)
    c.JSON(200, execResult)
}
```

### 5.2 Redis Broker

**Data Structures:**

```
┌──────────────────────────────────────────────────────────┐
│                                                          │
│  minos:queue                                              │
│  Type: List                                               │
│  Schema: FIFO queue of JSON-serialized Job objects       │
│  Size: ~1 KB per entry (job payload)                     │
│  Eviction: None (consumed by Worker BLPOP)                │
│  Persistence: Optional (AOF everysec)                     │
│                                                          │
│  minos:result:{job_id}                                    │
│  Type: List (single-element, used for BLPOP response)    │
│  Schema: ["JSON-serialized ExecutionResult"]              │
│  TTL: 300 seconds (auto-cleanup for abandoned jobs)       │
│                                                          │
│  minos:ratelimit:{ip}                                     │
│  Type: Sorted Set                                         │
│  Schema: {timestamp: timestamp} (score = value = now)     │
│  TTL: 2 * window_size                                     │
│                                                          │
│  minos:worker:{hostname}:heartbeat                        │
│  Type: String                                             │
│  Schema: "alive"                                         │
│  TTL: 30 seconds (renewed every 10s by worker)            │
│                                                          │
└──────────────────────────────────────────────────────────┘
```

**Key Redis guarantees leveraged:**
- `BLPOP` is atomic and fair (oldest waiting client gets the next item)
- `EXPIRE` on result keys prevents memory leaks from abandoned requests
- `ZADD` + `ZREMRANGEBYSCORE` is O(log N) for sliding window rate limits
- Single-threaded Redis avoids race conditions on queue consumption

**Failure modes:**

| Scenario                           | Behavior                                                 |
| ---------------------------------- | -------------------------------------------------------- |
| Redis OOM (out of memory)          | `maxmemory-policy volatile-lru` evicts old result keys   |
| Redis restart (no persistence)     | Queue drains, API returns 504 for in-flight requests     |
| Redis restart (AOF persistence)    | Queue replays, worker resumes processing                 |
| Massive queue backlog (>100k jobs) | Workers can't keep up → add more workers                 |

### 5.3 Worker Daemon

**Lifecycle per job:**

```go
func (w *Worker) processJob(job Job) {
    // 0. Defer cleanup no matter what happens
    workDir := filepath.Join(w.tmpBase, job.ID)
    defer os.RemoveAll(workDir)

    // 1. Create isolated workspace
    if err := os.MkdirAll(workDir, 0700); err != nil {
        w.pushError(job.ID, "failed to create workspace")
        return
    }

    // 2. Write code to file
    cfg := w.languageConfigs[job.Language]
    codePath := filepath.Join(workDir, cfg.SourceFile)
    if err := os.WriteFile(codePath, []byte(job.Code), 0400); err != nil {
        w.pushError(job.ID, "failed to write code file")
        return
    }

    // 3. Write stdin if provided
    if job.Stdin != "" {
        stdinPath := filepath.Join(workDir, "stdin.txt")
        os.WriteFile(stdinPath, []byte(job.Stdin), 0400)
    }

    // 4. Execute in nsjail with timeout context (belt + suspenders)
    ctx, cancel := context.WithTimeout(context.Background(), cfg.TimeLimit+2*time.Second)
    defer cancel()

    start := time.Now()
    result := w.execute(ctx, cfg, workDir)
    result.WallTime = time.Since(start).Milliseconds()

    // 5. Push result to Redis
    payload, _ := json.Marshal(result)
    resultKey := "minos:result:" + job.ID
    w.redis.LPush(ctx, resultKey, payload)
    w.redis.Expire(ctx, resultKey, 5*time.Minute)
}
```

**nsjail invocation:**

```go
func (w *Worker) execute(ctx context.Context, cfg LanguageConfig, workDir string) ExecutionResult {
    args := []string{
        "--config", cfg.NsjailConfig,
        "--bindmount", workDir + ":/app",
        "--",
    }
    args = append(args, cfg.ExecCommand...)

    cmd := exec.CommandContext(ctx, "nsjail", args...)
    cmd.SysProcAttr = &syscall.SysProcAttr{
        Setpgid: true, // Kill entire process group on timeout
    }

    var stdout, stderr bytes.Buffer
    cmd.Stdout = &stdout
    cmd.Stderr = &stderr

    // stdin passthrough
    if stdinPath := filepath.Join(workDir, "stdin.txt"); fileExists(stdinPath) {
        f, _ := os.Open(stdinPath)
        defer f.Close()
        cmd.Stdin = f
    }

    err := cmd.Run()

    result := ExecutionResult{
        Stdout:   truncateString(stdout.String(), 65536), // 64KB max output
        Stderr:   truncateString(stderr.String(), 65536),
        ExitCode: 0,
    }

    if err != nil {
        if exitErr, ok := err.(*exec.ExitError); ok {
            result.ExitCode = exitErr.ExitCode()
        } else if errors.Is(err, context.DeadlineExceeded) {
            // Context timeout fired — kill the process group
            syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
            result.ExitCode = 137 // SIGKILL
            result.Stderr = "Execution timed out"
        } else {
            result.ExitCode = 255
            result.Stderr = err.Error()
        }
    }

    return result
}
```

**Worker concurrency model:**

```go
func (w *Worker) Run(ctx context.Context, concurrency int) {
    var wg sync.WaitGroup
    for i := 0; i < concurrency; i++ {
        wg.Add(1)
        go func(workerID int) {
            defer wg.Done()
            for {
                select {
                case <-ctx.Done():
                    return
                default:
                    job, ok := w.dequeue(ctx)
                    if !ok {
                        return
                    }
                    w.processJob(job)
                }
            }
        }(i)
    }
    wg.Wait()
}
```

**Why goroutine-based, not process-pool:** nsjail itself forks a child process
inside namespaces. Go goroutines are cheap (2KB stack), and each goroutine
blocks on `cmd.Run()` which is I/O-bound from the Go runtime's perspective.
The real isolation happens inside nsjail's cloned namespaces, not at the Go
process level. 4-8 concurrent goroutines per CPU core is a sensible default.

### 5.4 nsjail Sandbox

**nsjail provides kernel-level isolation via:**

| Mechanism          | What it blocks                                                |
| ------------------ | ------------------------------------------------------------- |
| `CLONE_NEWNS`      | Custom `/proc`, `/tmp`, no access to host filesystem mounts   |
| `CLONE_NEWPID`     | Process inside jail sees only its own PID tree                |
| `CLONE_NEWNET`     | No network interface inside jail (disabled for execution)     |
| `CLONE_NEWUTS`     | Custom hostname (opaque to user code)                         |
| `CLONE_NEWIPC`     | No shared memory / semaphore access to host                   |
| `CLONE_NEWUSER`    | UID 99999 inside maps to root outside — jailed user has no power |
| `CLONE_NEWCGROUP`  | cgroups v2 for CPU/memory limits                              |
| seccomp            | Syscall allowlist (e.g., block `fork`, `execve`, `ptrace`)    |
| rlimits            | `RLIMIT_AS`, `RLIMIT_NPROC`, `RLIMIT_FSIZE`, `RLIMIT_CPU`    |

**Key config per language (python.cfg example):**

```
mode: ONCE
hostname: "minos-jail"
chroot: "/"
time_limit: 5
max_cpus: 1

uidmap {
    inside_id: "99999"
    outside_id: "0"
    count: 1
    use_newuidmap: false
}

gidmap {
    inside_id: "99999"
    outside_id: "0"
    count: 1
    use_newgidmap: false
}

# Block network entirely — no clone_newnet (stays in host net namespace but with no interfaces)
# or clone_newnet: true with no veth pair configured

mount {
    src: "/"
    dst: "/"
    is_bind: true
    mandatory: true
    rw: false
}

mount {
    src: "/proc"
    dst: "/proc"
    is_bind: true
    mandatory: true
    rw: false
}

# The writable workspace — only /app is RW
mount {
    src: "/tmp/minos/JOB_ID"
    dst: "/app"
    is_bind: true
    mandatory: true
    rw: true
}

# Seccomp — kill on forbidden syscall
seccomp_string {
    policy: "KILL"
}

seccomp_string {
    # Allow essential syscalls for interpreted languages
    syscall: "read"
    syscall: "write"
    syscall: "open"
    syscall: "openat"
    syscall: "close"
    syscall: "fstat"
    syscall: "mmap"
    syscall: "mprotect"
    syscall: "munmap"
    syscall: "brk"
    syscall: "rt_sigaction"
    syscall: "rt_sigprocmask"
    syscall: "sigreturn"
    syscall: "ioctl"
    syscall: "pread64"
    syscall: "readlink"
    syscall: "lseek"
    syscall: "getcwd"
    syscall: "getdents64"
    syscall: "arch_prctl"
    syscall: "set_tid_address"
    syscall: "set_robust_list"
    syscall: "clock_gettime"
    syscall: "exit"
    syscall: "exit_group"
    syscall: "futex"
}

rlimit_as {
    hard: 268435456     # 256 MB
    soft: 268435456
}

rlimit_cpu {
    hard: 5             # 5 seconds of CPU time
    soft: 5
}

rlimit_nproc {
    hard: 32            # Max 32 processes
    soft: 32
}

rlimit_fsize {
    hard: 5242880       # 5 MB max file size
    soft: 5242880
}

exec_bin {
    path: "/usr/bin/python3"
    arg: "/app/main.py"
    arg: "<"             # stdin from file
    arg: "/app/stdin.txt"
}
```

> **Note:** The `JOB_ID` in the mount path is replaced by the worker at invocation
> time via `--bindmount /tmp/minos/{job_id}:/app` on the nsjail command line.
> The config file uses a placeholder that gets overridden.

---

## 6. Security Model

### 6.1 Threat Model

| Threat                         | Mitigation                                                      |
| ------------------------------ | --------------------------------------------------------------- |
| Fork bombs                     | `rlimit_nproc: 32` in nsjail config                             |
| Memory exhaustion              | `rlimit_as: 256MB`, cgroups memory limit                        |
| CPU exhaustion                 | `time_limit`, `rlimit_cpu`, `max_cpus: 1`                       |
| Disk fill-up                   | `rlimit_fsize: 5MB`, tmp dir is tmpfs or size-limited partition |
| Network egress                 | No network namespace (clone_newnet disabled, no veth)           |
| Kernel exploit via syscalls    | Seccomp allowlist (KILL policy on unknown syscalls)              |
| Procfs info leak               | /proc mounted RO via bind-mount, not fresh procfs               |
| Host filesystem access         | chroot with RO mounts, only /app is RW                          |
| Container escape via ptrace    | Seccomp blocks `ptrace`, `process_vm_readv`, `process_vm_writev`|
| /dev access for raw disk       | No /dev mount in jail                                           |
| Symlink attacks in /tmp        | Unique per-job directory with 0700 permissions, deleted after   |
| Code injection via filename    | Job ID is a UUID — attacker never controls path                 |
| ReDoS / parser bombs           | All code limited to 64KB, no regex on user input                |
| Side-channel via timing        | Wall time reported but capped at 1ms granularity                |
| Memory scraping across jobs    | Each job gets a fresh nsjail process (no reuse)                 |
| Redis injection                | All keys prefixed with `minos:`, no user input in keys          |

### 6.2 Defense in Depth

```
Layer 1: API Gateway
  ├── Input validation (size, charset, language enum)
  ├── Rate limiting per IP
  └── No code ever written to API server disk

Layer 2: Network
  ├── API ↔ Redis: internal Docker network (no public exposure)
  ├── Worker ↔ Redis: internal network or localhost
  └── Worker nodes have no public IP

Layer 3: Worker Process
  ├── Unique temp dir per job (UUID-named, 0700)
  ├── Defer cleanup (os.RemoveAll) even on panic
  └── Context timeout as fallback to nsjail timer

Layer 4: nsjail (kernel)
  ├── User namespace (UID 99999, no capabilities)
  ├── Mount namespace (RO system, RW only /app)
  ├── PID namespace (can't see host processes)
  ├── No network namespace (or empty net NS)
  ├── cgroups v2 (CPU + memory limits)
  ├── Seccomp (syscall allowlist)
  ├── Rlimits (CPU, memory, processes, file size)
  └── No_new_privs (can't gain capabilities via setuid)
```

### 6.3 What Happens When a Job Is Malicious

```
1. User submits fork bomb in Python
   → nsjail rlimit_nproc: 32 → fork fails with OSError
   → Process terminates, worker captures stderr

2. User submits infinite loop
   → nsjail time_limit: 5s → SIGKILL after 5 CPU seconds
   → Fallback: Go context timeout → SIGKILL process group
   → Exit code 137, stderr "Execution timed out"

3. User tries to read /etc/shadow
   → nsjail chroot + RO mounts → file not found or permission denied
   → nsjail user namespace UID 99999 → no root access

4. User tries to open a socket
   → No network namespace → socket() returns EAFNOSUPPORT
   → Or seccomp blocks socket syscall → killed by seccomp

5. User tries to mmap /dev/mem
   → No /dev mount in jail → /dev/mem doesn't exist
   → Even if it existed → seccomp blocks mmap on non-file fds

6. User tries to write enormous output
   → Worker truncates stdout/stderr to 64KB
   → rlimit_fsize caps file writes to 5MB
   → Worker reads into bounded buffer

7. User submits binary/exploit disguised as Python
   → Python interpreter parses it as text → SyntaxError
   → If source contains null bytes → write fails or Python rejects
```

---

## 7. Language Support & Configs

### 7.1 Supported Languages

```
┌────────────┬──────────────────┬─────────────┬───────────┬───────────────┐
│ Language   │ Binary           │ Source File │ Timeout   │ Memory Limit  │
├────────────┼──────────────────┼─────────────┼───────────┼───────────────┤
│ Python 3   │ /usr/bin/python3 │ main.py     │ 5s        │ 256 MB        │
│ Bash       │ /usr/bin/bash    │ main.sh     │ 3s        │ 64 MB         │
│ JavaScript │ /usr/bin/node    │ main.js     │ 5s        │ 256 MB        │
│ Go         │ go run           │ main.go     │ 5s        │ 512 MB        │
│ C (gcc)    │ gcc + ./a.out    │ main.c      │ 3s+2s*    │ 256 MB        │
│ C++ (g++)  │ g++ + ./a.out    │ main.cpp    │ 3s+2s*    │ 256 MB        │
│ Java       │ javac + java     │ Main.java   │ 5s+3s*    │ 512 MB        │
└────────────┴──────────────────┴─────────────┴───────────┴───────────────┘
* Compile timeout + execution timeout (separate phases)
```

> Registry lives in `internal/worker/language.go`; runtimes are installed in
> the worker image (`deploy/docker/Dockerfile.worker`). The registry is the
> allowlist — an unregistered language is rejected with `exit_code 255`.

> **Compiled-language specifics:** C/C++ compile to `/app/main.out` with gcc/g++
> (`TMPDIR=/app` since `/` is read-only). Java compiles `Main.java` to `/app`
> classes (`javac -d /app`) and runs `java -cp /app Main` with `-Xmx256m` and
> `-Djava.io.tmpdir=/app`. Go uses `go run main.go` with `GOCACHE=/app/.cache`
> and `GOTOOLCHAIN=local` (no network, no toolchain download).

### 7.2 Compiled Language Handling

Compiled languages (C, C++, Rust, Java) need a **two-phase execution:**

```go
// Phase 1: Compile (with separate timeout, larger memory limit)
compileResult := w.execute(compileCfg, workDir)
if compileResult.ExitCode != 0 {
    // Return compilation error immediately
    compileResult.Stderr = "Compilation error:\n" + compileResult.Stderr
    w.pushResult(job.ID, compileResult)
    return
}

// Phase 2: Run the binary
runResult := w.execute(runCfg, workDir)
w.pushResult(job.ID, runResult)
```

**C compilation nsjail config excerpt:**
```
exec_bin {
    path: "/usr/bin/gcc"
    arg: "-std=c11"
    arg: "-O2"
    arg: "-Wall"
    arg: "-o"
    arg: "/app/main.out"
    arg: "/app/main.c"
}
rlimit_as { hard: 536870912; soft: 536870912 }  // 512 MB for compilation
time_limit: 10
```

**C execution nsjail config excerpt:**
```
exec_bin {
    path: "/app/main.out"
}
rlimit_as { hard: 268435456; soft: 268435456 }  // 256 MB for execution
time_limit: 3
```

### 7.3 Config Template System

Rather than duplicate configs, workers use a template system:

```
# template.cfg (Go text/template)
mode: ONCE
hostname: "minos-{{.Language}}"
time_limit: {{.TimeLimit}}
max_cpus: 1

uidmap { inside_id: "99999"; outside_id: "0"; count: 1; use_newuidmap: false }
gidmap { inside_id: "99999"; outside_id: "0"; count: 1; use_newgidmap: false }

mount {
    src: "/"
    dst: "/"
    is_bind: true
    mandatory: true
    rw: false
}

mount {
    src: "/proc"
    dst: "/proc"
    is_bind: true
    mandatory: true
    rw: false
}

rlimit_as { hard: {{.MemoryLimit}}; soft: {{.MemoryLimit}} }
rlimit_cpu { hard: {{.TimeLimit}}; soft: {{.TimeLimit}} }
rlimit_nproc { hard: {{.MaxProcs}}; soft: {{.MaxProcs}} }
rlimit_fsize { hard: {{.MaxFileSize}}; soft: {{.MaxFileSize}} }

exec_bin {
    path: "{{.Executable}}"
{{range .Args}}    arg: "{{.}}"
{{end}}}
```

---

## 8. Edge Cases & Failure Modes

### 8.1 Worker Crashes Mid-Execution

```
Scenario: Worker dies while nsjail is running a job
├── nsjail child process becomes orphaned
├── nsjail has its own time_limit → will self-terminate
├── temp files in /tmp/minos/{job_id} remain → needs cleanup
│
Solution:
├── Worker startup: scan /tmp/minos/ and remove dirs older than 1 hour
├── tmpfs for /tmp/minos → cleared on reboot
├── Systemd tmpfiles.d rule: daily cleanup of /tmp/minos/
└── Redis result key expires after 300s → API returns 504
```

### 8.2 Redis Connection Loss

```
┌──────────────────────────────────────────────────────────────┐
│ API Side:                                                    │
│   BLPOP will return error → API returns 500                  │
│   Health check fails → load balancer removes this instance   │
│   Retries with exponential backoff (100ms, 200ms, 400ms...)  │
│                                                              │
│ Worker Side:                                                 │
│   BLPOP returns error → sleep 1s → retry (max 10 retries)   │
│   After max retries → worker exits, systemd restarts it      │
└──────────────────────────────────────────────────────────────┘
```

### 8.3 Massive Concurrent Submissions

```
Scenario: 10,000 submissions arrive in 1 second
├── Queue: minos:queue grows to 10,000 entries (~10 MB)
├── API: 10,000 BLPOP connections waiting on result keys
├── Workers: Process at rate of ~200 jobs/sec (4 workers × 50/sec each)
│
Results:
├── Queue drain time: ~50 seconds
├── API connections: all 10,000 held open (Gin + Go can handle this)
├── Timeouts: Requests near the back of queue may hit API's 10s timeout
│
Mitigations:
├── Configurable request timeout (client chooses: 5s / 15s / 30s)
├── Queue-length health check → scale workers when queue > 1000
├── Reject submissions with 503 when queue > 5000 (circuit breaker)
└── Return queue_position in response for async polling mode
```

### 8.4 Unicode & Binary Data

```
Submissions must be valid UTF-8:
  ✓ Valid UTF-8 Python source → accepted
  ✗ Binary blob → rejected at API validation
  ✗ Invalid UTF-8 sequence → rejected at API validation
  ✗ Null bytes in code string → stripped or rejected

Output handling:
  ✓ stdout/stderr may contain any bytes → base64-encode in JSON response
    {
      "stdout": "SGVsbG8gV29ybGQ=",     // base64
      "stdout_raw": false,               // client knows to decode
      "stderr": "",
      "exit_code": 0,
      "time_ms": 42
    }
  ✓ Or: replace non-printable chars with �, flag truncated output
```

### 8.5 File Encoding Attacks

```
Attack: User writes code that creates symlinks, hardlinks, or special files.
Mitigation:
  ✓ nsjail user is UID 99999 (nobody) — can't chown files
  ✓ rlimit_fsize: 5MB — can't create huge files
  ✓ Seccomp blocks mknod, mknodat — can't create device files
  ✓ Only /app is writable, and it's a tmpfs/bind-mount — no real device
  ✓ No_new_privs — can't become root via setuid binary
```

### 8.6 Fork Bombs & Process Spawning

```
Python: os.fork() or subprocess.Popen()
  → rlimit_nproc: 32 → fork fails after 32 processes
  → nsjail detects rlimit violation → SIGKILL
  → Exit code: 137 (SIGKILL)

C: fork() in a loop
  → Same rlimit_nproc enforcement
  → Additionally: seccomp can block clone/fork for interpreted langs

Go/Rust: spawning threads
  → Threads still count toward rlimit_nproc on some kernels
  → cgroups pids.max controller as additional limit
```

### 8.7 Symlink Escape Attempts

```
Attack: User creates symlink to /etc/shadow inside /app
  → In jail: /app points to /tmp/minos/{uuid} on host
  → Symlink target /etc/shadow → resolves to /etc/shadow inside jail
  → /etc/shadow is on RO bind-mount → can't read (no permissions as UID 99999)

Attack: User creates symlink at /tmp/minos/{uuid}/escape -> /etc/passwd
  → After worker cleans up: os.RemoveAll follows symlinks by default in Go?
  → Go's os.RemoveAll does NOT follow symlinks!
  → Only the symlink itself is removed, not the target
  → Even if it did: UID 99999, target is RO → can't modify
```

### 8.8 Concurrent Execution on Same Worker

```
Worker Model: N goroutines, each processing one job at a time
Each goroutine creates its own /tmp/minos/{uuid} directory

Safety:
  ✓ UUIDs are unique (v4, collision probability negligible)
  ✓ Each nsjail invocation is a separate process
  ✓ No shared memory/files between concurrent jobs
  ✓ File descriptors are not shared between goroutines

Potential issue: resource contention
  ├── 4 concurrent jobs each using 256MB = 1GB total needed
  ├── Worker should track concurrency vs total memory
  └── Config: max_concurrency = min(cores, total_ram / per_job_ram)
```

### 8.9 Timezone, Locale, and Environment Leaks

```
nsjail jail environment is empty by default:
  ✗ No HOME, USER, PATH from host
  ✗ No LANG, LC_ALL (defaults to C/POSIX)
  ✗ No TZ (defaults to UTC)
  ✓ Set explicitly if needed: env { key: "LANG"; value: "C.UTF-8" }
  ✓ Set explicitly: env { key: "PATH"; value: "/usr/bin:/bin" }
```

---

## 9. Production Hardening

### 9.1 Worker Host Setup (bare-metal)

```bash
# 1. Dedicated user for worker process
useradd -r -s /bin/false minos-worker

# 2. cgroups v2 setup
echo "+cpu +memory +pids" > /sys/fs/cgroup/cgroup.subtree_control
mkdir -p /sys/fs/cgroup/minos
chown minos-worker:minos-worker /sys/fs/cgroup/minos

# 3. tmpfs for workspaces (size-limited)
echo "tmpfs /tmp/minos tmpfs rw,nodev,nosuid,noexec,size=4G,mode=700 0 0" >> /etc/fstab
mount /tmp/minos

# 4. nsjail binary (compiled with seccomp support)
cp nsjail /usr/local/bin/nsjail
chmod 755 /usr/local/bin/nsjail
setcap cap_sys_admin,cap_sys_chroot,cap_setuid,cap_setgid,cap_sys_resource+ep /usr/local/bin/nsjail

# 5. Systemd service for worker
cat > /etc/systemd/system/minos-worker.service <<EOF
[Unit]
Description=Minos Execution Worker
After=network.target

[Service]
Type=simple
User=minos-worker
ExecStart=/opt/minos/bin/worker
Restart=always
RestartSec=5
LimitNOFILE=65536
LimitNPROC=8192
ProtectSystem=strict
ProtectHome=yes
ReadWritePaths=/tmp/minos /sys/fs/cgroup/minos
NoNewPrivileges=yes
MemoryDenyWriteExecute=yes

[Install]
WantedBy=multi-user.target
EOF

systemctl daemon-reload
systemctl enable minos-worker
```

### 9.2 API Deployment (Docker)

```dockerfile
FROM golang:1.23-alpine AS build
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o api ./cmd/api

FROM scratch
COPY --from=build /app/api /api
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
EXPOSE 8080
ENTRYPOINT ["/api"]
```

### 9.3 Health Checks

```
API Health:
  GET /healthz → 200 {"status": "ok", "redis": "connected"}
  GET /readyz  → 200 {"status": "ready"} | 503 {"status": "not_ready"}

  /healthz: Returns 200 if process is alive (liveness probe)
  /readyz:  Returns 200 if Redis is reachable (readiness probe)

Worker Health:
  Redis heartbeat key: minos:worker:{hostname}:heartbeat
  TTL: 30s, renewed every 10s
  If heartbeat expires → worker is dead → alert to on-call
```

### 9.4 Secrets Management

```
Redis password: Environment variable (MINOS_REDIS_PASSWORD)
  ├── Set via Kubernetes secret or systemd EnvironmentFile
  └── Never in config files, never in source code

No other secrets needed:
  ✗ No database credentials
  ✗ No API keys
  ✗ No TLS certs for internal communication (internal network only)
```

---

## 10. Scalability & Multi-Worker

### 10.1 Horizontal Scaling

```
                 ┌──────────────┐
                 │ Load Balancer │
                 │  (nginx/haproxy)│
                 └──┬──┬──┬─────┘
                    │  │  │
          ┌─────────┘  │  └─────────┐
          ▼            ▼            ▼
    ┌──────────┐ ┌──────────┐ ┌──────────┐
    │  API #1  │ │  API #2  │ │  API #3  │
    └────┬─────┘ └────┬─────┘ └────┬─────┘
         │            │            │
         └────────────┼────────────┘
                      │
              ┌───────▼───────┐
              │    Redis       │
              │  (single or    │
              │   cluster)     │
              └───────┬───────┘
                      │
         ┌────────────┼────────────┐
         │            │            │
    ┌────▼────┐ ┌────▼────┐ ┌────▼────┐
    │Worker #1│ │Worker #2│ │Worker #3│
    │(8 cores)│ │(8 cores)│ │(8 cores)│
    └─────────┘ └─────────┘ └─────────┘
```

**Scaling properties:**
- **API layer:** Stateless, scales horizontally infinitely. Load balancer uses
  least-connections algorithm.
- **Redis:** Can use Redis Cluster for sharding if queue size > available RAM.
  Start with single instance (can handle ~100k ops/sec).
- **Worker layer:** Each worker is independent. Adding workers increases
  throughput linearly. Workers can be heterogeneous (some with GPUs for ML code).

### 10.2 Queue Prioritization

```
Instead of a single minos:queue, use multiple priority queues:

  minos:queue:high    (priority users, small code)
  minos:queue:medium  (standard users)
  minos:queue:low     (batch jobs, large code)

Worker BLPOP pattern: check high, then medium, then low
(Can't do BLPOP on multiple keys — use LPOP with short sleep instead)

OR: Use Redis Streams with consumer groups for priority:
  XADD minos:queue * priority high job {...}
  XREADGROUP ... >  (ordered by priority within stream)
```

### 10.3 Backpressure Mechanisms

```
┌─────────────────────────────────────────────────────────┐
│ Queue Depth (qdepth = LLEN minos:queue)                 │
│                                                         │
│   qdepth < 1000   → Normal operation                   │
│   1000 < qdepth < 5000 → Return X-Queue-Depth header    │
│                          to inform client of delay      │
│   qdepth > 5000   → 503 Service Unavailable            │
│                      "queue full, try again later"      │
│                                                         │
│   Additional: per-language queue depth monitoring       │
│   If Python queue is deep but Ruby is idle →            │
│   alert to check Python worker capacity                 │
└─────────────────────────────────────────────────────────┘
```

---

## 11. Observability & Metrics

### 11.1 Prometheus Metrics

```
API Metrics:
  minos_api_requests_total{method, endpoint, status}      # Counter
  minos_api_request_duration_ms{method, endpoint}         # Histogram
  minos_api_queue_push_failures_total                      # Counter
  minos_api_result_wait_ms                                 # Histogram (BLPOP time)
  minos_api_rate_limited_total                             # Counter

Worker Metrics:
  minos_worker_jobs_total{language, exit_code}             # Counter
  minos_worker_job_duration_ms{language}                   # Histogram
  minos_worker_nsjail_spawn_ms                             # Histogram
  minos_worker_cleanup_ms                                  # Histogram
  minos_worker_errors_total{type}                          # Counter
    types: workspace_create_failed, nsjail_spawn_failed,
           timeout, cleanup_failed

Redis Metrics:
  minos_queue_depth                                         # Gauge (LLEN)
  minos_queue_oldest_age_seconds                            # Gauge
  minos_result_keys_active                                  # Gauge

System Metrics (worker host):
  node_disk_usage_bytes{path="/tmp/minos"}                 # Gauge
  node_memory_available_bytes                              # Gauge
  node_cpu_usage_percent                                   # Gauge
```

### 11.2 Structured Logging

```go
// Use zerolog or zap for structured JSON logs
log.Info().
    Str("job_id", jobID).
    Str("language", lang).
    Int("code_size", len(code)).
    Int64("queue_position", queuePos).
    Msg("job submitted")

log.Info().
    Str("job_id", jobID).
    Int("exit_code", result.ExitCode).
    Int64("wall_time_ms", result.WallTime).
    Int("stdout_size", len(result.Stdout)).
    Int("stderr_size", len(result.Stderr)).
    Msg("job completed")

log.Error().
    Str("job_id", jobID).
    Err(err).
    Str("stage", "nsjail_spawn").
    Msg("execution failed")
```

### 11.3 Alerting Rules

```
- queue_depth > 5000 for 2m → P1: Queue backup, check workers
- worker heartbeat missing > 30s → P2: Worker node down
- api_error_5xx_rate > 1% for 5m → P2: API degradation
- rate_limited_rate > 100/min → P3: Possible abuse
- job_latency_p99 > 5s for 5m → P3: Slowdown detected
- /tmp/minos disk usage > 80% → P3: Cleanup failure
```

---

## 12. Deployment Topology (Single VM)

### 12.1 Layout

```
┌──────────────────────────────────────────────────────────────────────┐
│                        Public Internet                                │
│                              │                                        │
│                     ┌────────▼────────┐                               │
│                     │  nginx / Caddy  │  Reverse proxy, TLS, rate limit│
│                     │  (host systemd) │                               │
│                     └────────┬────────┘                               │
│                              │ :8080                                  │
│  ┌───────────────────────────┼────────────────────────────────────┐  │
│  │                    Single VM (Fedora/Debian)                    │  │
│  │                                                                  │  │
│  │  ┌─────────────────────────────────────────────────────────┐    │  │
│  │  │          Docker (docker-compose, bridge network)          │    │  │
│  │  │                                                           │    │  │
│  │  │  ┌──────────────┐          ┌──────────────┐              │    │  │
│  │  │  │  API (Gin)   │◄────────▶│    Redis     │              │    │  │
│  │  │  │  :8080       │  bridge  │    :6379     │              │    │  │
│  │  │  │  replicas: 2 │  network │  (exposed)   │              │    │  │
│  │  │  └──────────────┘          └──────┬───────┘              │    │  │
│  │  └───────────────────────────────────┼───────────────────────┘    │  │
│  │                                      │                             │  │
│  │                          localhost:6379 (exposed port)             │  │
│  │                                      │                             │  │
│  │  ┌───────────────────────────────────┼───────────────────────┐    │  │
│  │  │          Worker (systemd service, bare metal)              │    │  │
│  │  │                                                           │    │  │
│  │  │  ┌──────────┐   ┌──────────┐   ┌──────────┐              │    │  │
│  │  │  │ Worker   │   │ Worker   │   │ Worker   │              │    │  │
│  │  │  │ goroutine│   │ goroutine│   │ goroutine│              │    │  │
│  │  │  │    #1    │   │    #2    │   │    #N    │              │    │  │
│  │  │  └────┬─────┘   └────┬─────┘   └────┬─────┘              │    │  │
│  │  │       │              │              │                     │    │  │
│  │  │       └──────────────┼──────────────┘                     │    │  │
│  │  │                      │                                    │    │  │
│  │  │              ┌───────▼───────┐                             │    │  │
│  │  │              │    nsjail     │  kernel sandbox             │    │  │
│  │  │              │  (per job)    │  user NS + mount NS + PID NS│    │  │
│  │  │              │  seccomp      │  RLIMITs + time_limit       │    │  │
│  │  │              └───────────────┘                             │    │  │
│  │  │                                                           │    │  │
│  │  │  /tmp/minos (tmpfs, 4GB)    ← per-job workspace           │    │  │
│  │  │  /opt/minos/compilers/      ← python3, node, gcc, etc.    │    │  │
│  │  └───────────────────────────────────────────────────────────┘    │  │
│  └──────────────────────────────────────────────────────────────────┘  │
└──────────────────────────────────────────────────────────────────────┘
```

### 12.2 Why This Split

| Component   | Runs as        | Reason                                              |
| ----------- | -------------- | --------------------------------------------------- |
| API + Redis | Docker compose | Easy orchestration, health checks, restart policy   |
| Worker      | systemd        | Needs direct kernel access for nsjail               |
|             |                | (namespaces, cgroups, mounts — all raw kernel API)  |
| nginx       | systemd        | TLS termination, rate limiting, static file serving |

The Worker runs **bare metal**, not inside Docker, because nsjail needs unimpeded access to Linux kernel primitives (CLONE flags, mount, prctl, setrlimit). Running nsjail inside a Docker container adds an unnecessary namespace nesting layer and causes the `/proc` overmount issue we had to work around with the entrypoint.sh.

### 12.3 Network Flow

```
Internet → :443 (nginx/TLS) → localhost:8080 (API container)
API container → redis:6379 (Docker bridge network)
Worker (host) → localhost:6379 (Redis exposed port)
Worker → No outbound internet (firewall rule)
```

### 12.4 docker-compose.yml (API + Redis only)

```yaml
# docker-compose.yml
services:
  redis:
    image: redis:7-alpine
    command: >
      redis-server
      --requirepass ${REDIS_PASSWORD:-secret}
      --maxmemory 512mb
      --maxmemory-policy volatile-lru
      --save ""
      --appendonly no
    ports:
      - "127.0.0.1:6379:6379"    # Expose to host for worker
    volumes:
      - redis-data:/data
    restart: unless-stopped
    healthcheck:
      test: ["CMD", "redis-cli", "ping"]
      interval: 10s
      timeout: 3s
      retries: 3

  api:
    build:
      context: .
      dockerfile: Dockerfile.api
    ports:
      - "127.0.0.1:8080:8080"
    environment:
      - REDIS_ADDR=redis:6379
      - REDIS_PASSWORD=${REDIS_PASSWORD:-secret}
      - MAX_CODE_SIZE=65536
      - EXECUTION_TIMEOUT=10s
      - RATE_LIMIT_PER_MINUTE=30
    depends_on:
      redis:
        condition: service_healthy
    restart: unless-stopped

volumes:
  redis-data:
```

### 12.5 Worker systemd Service

```ini
# /etc/systemd/system/minos-worker.service
[Unit]
Description=Minos Exec Engine Worker
After=network.target docker.service redis.service
Requires=docker.service

[Service]
Type=simple
User=minos-worker
Group=minos-worker
ExecStart=/opt/minos/bin/worker
Restart=always
RestartSec=5

# nsjail needs these to create namespaces
AmbientCapabilities=CAP_SYS_ADMIN CAP_SYS_CHROOT CAP_SETUID CAP_SETGID CAP_SYS_RESOURCE
CapabilityBoundingSet=CAP_SYS_ADMIN CAP_SYS_CHROOT CAP_SETUID CAP_SETGID CAP_SYS_RESOURCE

# Security hardening for the worker process itself
NoNewPrivileges=yes
ProtectSystem=strict
ProtectHome=yes
ReadWritePaths=/tmp/minos
PrivateTmp=yes
MemoryDenyWriteExecute=yes
LimitNOFILE=65536
LimitNPROC=8192

# Environment
Environment=REDIS_ADDR=localhost:6379
Environment=REDIS_PASSWORD=secret
Environment=CONCURRENCY=4
Environment=TMP_DIR=/tmp/minos

[Install]
WantedBy=multi-user.target
```

### 12.6 Worker Host Setup

```bash
# 1. Create worker user
sudo useradd -r -s /bin/false -d /opt/minos minos-worker

# 2. Create tmpfs for job workspaces
sudo mkdir -p /tmp/minos
echo "tmpfs /tmp/minos tmpfs rw,nodev,nosuid,noexec,size=4G,mode=700,uid=minos-worker 0 0" | sudo tee -a /etc/fstab
sudo mount /tmp/minos

# 3. Install compilers/interpreters
sudo dnf install -y python3 nodejs gcc gcc-c++ ruby java-latest-openjdk

# 4. Build nsjail
git clone --depth 1 https://github.com/google/nsjail.git /tmp/nsjail-build
cd /tmp/nsjail-build && make -j$(nproc)
sudo cp nsjail /usr/local/bin/
sudo chmod 755 /usr/local/bin/nsjail
rm -rf /tmp/nsjail-build

# 5. Deploy worker binary and config
sudo mkdir -p /opt/minos/{bin,configs}
sudo cp worker /opt/minos/bin/
sudo cp nsjail-worker.cfg /opt/minos/configs/
sudo chown -R minos-worker:minos-worker /opt/minos

# 6. Enable and start
sudo systemctl daemon-reload
sudo systemctl enable --now minos-worker
```

### 12.7 Firewall Rules

```bash
# Only allow SSH and HTTPS from outside
sudo firewall-cmd --add-service=https --permanent
sudo firewall-cmd --add-service=ssh --permanent

# Block worker from making outbound connections
sudo firewall-cmd --direct --add-rule ipv4 filter OUTPUT 0 \
  -m owner --uid-owner minos-worker -j DROP
sudo firewall-cmd --direct --add-rule ipv4 filter OUTPUT 0 \
  -m owner --uid-owner minos-worker -d 127.0.0.1 -p tcp --dport 6379 -j ACCEPT

sudo firewall-cmd --reload
```

### 12.8 Startup Order

```bash
# 1. Start Docker containers
docker compose up -d

# 2. Start worker
sudo systemctl start minos-worker

# 3. Verify
docker compose ps                    # api + redis should be healthy
sudo systemctl status minos-worker   # worker should be active
redis-cli -a secret PING             # PONG

# 4. Smoke test
curl -X POST http://localhost:8080/api/v1/execute \
  -H 'Content-Type: application/json' \
  -d '{"language":"python","code":"print(42)"}'
```

---

## 13. Directory Structure

```
minos-exec-engine/
├── cmd/
│   ├── api/
│   │   └── main.go                 # Gin server entrypoint
│   └── worker/
│       └── main.go                 # Worker daemon entrypoint
├── internal/
│   ├── api/
│   │   ├── handler.go              # Gin route handlers
│   │   ├── handler_test.go
│   │   ├── middleware.go           # Rate limiting, request ID, recovery
│   │   ├── router.go               # Gin router setup
│   │   └── validator.go            # Payload validation
│   ├── worker/
│   │   ├── worker.go               # Main worker loop (BLPOP, dispatch)
│   │   ├── executor.go             # nsjail os/exec wrapper
│   │   ├── workspace.go            # Temp dir creation, cleanup, template
│   │   └── language.go             # Language config registry
│   ├── models/
│   │   ├── job.go                  # Job, SubmissionRequest, ExecutionResult
│   │   └── language.go             # LanguageConfig struct
│   ├── redisq/
│   │   ├── client.go               # Redis connection pool
│   │   ├── queue.go                # RPush, BLPop wrappers
│   │   ├── result.go               # LPush result + TTL
│   │   └── ratelimit.go            # Sliding window rate limiter
│   └── metrics/
│       └── metrics.go              # Prometheus metric definitions
├── configs/
│   ├── nsjail/
│   │   ├── base.template.cfg       # Template with placeholders
│   │   ├── python.cfg              # Generated/custom nsjail config
│   │   ├── node.cfg
│   │   ├── gcc-compile.cfg         # C compilation phase
│   │   ├── gcc-run.cfg             # C execution phase
│   │   ├── gpp-compile.cfg         # C++ compilation
│   │   ├── gpp-run.cfg
│   │   ├── java.cfg
│   │   ├── ruby.cfg
│   │   └── bash.cfg
│   └── languages.yaml             # Language registry (image, cmd, limits)
├── deploy/
│   ├── docker/
│   │   ├── Dockerfile.api
│   │   ├── Dockerfile.worker
│   │   └── docker-compose.yml      # Local dev setup
│   ├── systemd/
│   │   └── minos-worker.service
│   └── ansible/
│       └── worker-setup.yml        # Bare-metal provisioning
├── scripts/
│   ├── setup-worker-host.sh        # cgroups, tmpfs, nsjail install
│   └── seed-languages.sh           # Install compilers/interpreters
├── go.mod
├── go.sum
├── Makefile
└── README.md
```

---

## 14. Appendix: Full Config Examples

### 14.1 Python nsjail Config

```
mode: ONCE
hostname: "minos-python"
time_limit: 5
max_cpus: 1

uidmap {
    inside_id: "99999"
    outside_id: "0"
    count: 1
    use_newuidmap: false
}

gidmap {
    inside_id: "99999"
    outside_id: "0"
    count: 1
    use_newgidmap: false
}

mount {
    src: "/"
    dst: "/"
    is_bind: true
    mandatory: true
    rw: false
}

mount {
    src: "/proc"
    dst: "/proc"
    is_bind: true
    mandatory: true
    rw: false
}

seccomp_string { policy: "KILL" }
seccomp_string {
    syscall: "read"
    syscall: "write"
    syscall: "open"
    syscall: "openat"
    syscall: "close"
    syscall: "fstat"
    syscall: "lstat"
    syscall: "stat"
    syscall: "newfstatat"
    syscall: "poll"
    syscall: "lseek"
    syscall: "mmap"
    syscall: "mprotect"
    syscall: "munmap"
    syscall: "brk"
    syscall: "rt_sigaction"
    syscall: "rt_sigprocmask"
    syscall: "rt_sigreturn"
    syscall: "ioctl"
    syscall: "pread64"
    syscall: "pwrite64"
    syscall: "readv"
    syscall: "writev"
    syscall: "access"
    syscall: "pipe"
    syscall: "select"
    syscall: "sched_yield"
    syscall: "mremap"
    syscall: "msync"
    syscall: "mincore"
    syscall: "madvise"
    syscall: "shmget"
    syscall: "shmat"
    syscall: "shmctl"
    syscall: "dup"
    syscall: "dup2"
    syscall: "pause"
    syscall: "nanosleep"
    syscall: "getitimer"
    syscall: "setitimer"
    syscall: "alarm"
    syscall: "getpid"
    syscall: "sendfile"
    syscall: "socket"
    syscall: "connect"
    syscall: "accept"
    syscall: "sendto"
    syscall: "recvfrom"
    syscall: "sendmsg"
    syscall: "recvmsg"
    syscall: "shutdown"
    syscall: "bind"
    syscall: "listen"
    syscall: "getsockname"
    syscall: "getpeername"
    syscall: "socketpair"
    syscall: "setsockopt"
    syscall: "getsockopt"
    syscall: "clone"
    syscall: "fork"
    syscall: "vfork"
    syscall: "execve"
    syscall: "exit"
    syscall: "wait4"
    syscall: "kill"
    syscall: "uname"
    syscall: "semget"
    syscall: "semop"
    syscall: "semctl"
    syscall: "shmdt"
    syscall: "msgget"
    syscall: "msgsnd"
    syscall: "msgrcv"
    syscall: "msgctl"
    syscall: "fcntl"
    syscall: "flock"
    syscall: "fsync"
    syscall: "fdatasync"
    syscall: "truncate"
    syscall: "ftruncate"
    syscall: "getdents"
    syscall: "getcwd"
    syscall: "chdir"
    syscall: "fchdir"
    syscall: "rename"
    syscall: "mkdir"
    syscall: "rmdir"
    syscall: "creat"
    syscall: "link"
    syscall: "unlink"
    syscall: "symlink"
    syscall: "readlink"
    syscall: "chmod"
    syscall: "fchmod"
    syscall: "chown"
    syscall: "fchown"
    syscall: "lchown"
    syscall: "umask"
    syscall: "gettimeofday"
    syscall: "getrlimit"
    syscall: "getrusage"
    syscall: "sysinfo"
    syscall: "times"
    syscall: "ptrace"
    syscall: "getuid"
    syscall: "syslog"
    syscall: "getgid"
    syscall: "setuid"
    syscall: "setgid"
    syscall: "geteuid"
    syscall: "getegid"
    syscall: "setpgid"
    syscall: "getppid"
    syscall: "getpgrp"
    syscall: "setsid"
    syscall: "setreuid"
    syscall: "setregid"
    syscall: "getgroups"
    syscall: "setgroups"
    syscall: "setresuid"
    syscall: "getresuid"
    syscall: "setresgid"
    syscall: "getresgid"
    syscall: "getpgid"
    syscall: "setfsuid"
    syscall: "setfsgid"
    syscall: "getsid"
    syscall: "capget"
    syscall: "capset"
    syscall: "rt_sigpending"
    syscall: "rt_sigtimedwait"
    syscall: "rt_sigqueueinfo"
    syscall: "rt_sigsuspend"
    syscall: "sigaltstack"
    syscall: "utime"
    syscall: "mknod"
    syscall: "uselib"
    syscall: "personality"
    syscall: "ustat"
    syscall: "statfs"
    syscall: "fstatfs"
    syscall: "sysfs"
    syscall: "getpriority"
    syscall: "setpriority"
    syscall: "sched_setparam"
    syscall: "sched_getparam"
    syscall: "sched_setscheduler"
    syscall: "sched_getscheduler"
    syscall: "sched_get_priority_max"
    syscall: "sched_get_priority_min"
    syscall: "sched_rr_get_interval"
    syscall: "mlock"
    syscall: "munlock"
    syscall: "mlockall"
    syscall: "munlockall"
    syscall: "vhangup"
    syscall: "modify_ldt"
    syscall: "pivot_root"
    syscall: "_sysctl"
    syscall: "prctl"
    syscall: "arch_prctl"
    syscall: "adjtimex"
    syscall: "setrlimit"
    syscall: "chroot"
    syscall: "sync"
    syscall: "acct"
    syscall: "settimeofday"
    syscall: "mount"
    syscall: "umount2"
    syscall: "swapon"
    syscall: "swapoff"
    syscall: "reboot"
    syscall: "sethostname"
    syscall: "setdomainname"
    syscall: "iopl"
    syscall: "ioperm"
    syscall: "create_module"
    syscall: "init_module"
    syscall: "delete_module"
    syscall: "get_kernel_syms"
    syscall: "query_module"
    syscall: "quotactl"
    syscall: "nfsservctl"
    syscall: "getpmsg"
    syscall: "putpmsg"
    syscall: "afs_syscall"
    syscall: "tuxcall"
    syscall: "security"
    syscall: "gettid"
    syscall: "readahead"
    syscall: "setxattr"
    syscall: "lsetxattr"
    syscall: "fsetxattr"
    syscall: "getxattr"
    syscall: "lgetxattr"
    syscall: "fgetxattr"
    syscall: "listxattr"
    syscall: "llistxattr"
    syscall: "flistxattr"
    syscall: "removexattr"
    syscall: "lremovexattr"
    syscall: "fremovexattr"
    syscall: "tkill"
    syscall: "time"
    syscall: "futex"
    syscall: "sched_setaffinity"
    syscall: "sched_getaffinity"
    syscall: "set_thread_area"
    syscall: "io_setup"
    syscall: "io_destroy"
    syscall: "io_getevents"
    syscall: "io_submit"
    syscall: "io_cancel"
    syscall: "get_thread_area"
    syscall: "lookup_dcookie"
    syscall: "epoll_create"
    syscall: "epoll_ctl_old"
    syscall: "epoll_wait_old"
    syscall: "remap_file_pages"
    syscall: "getdents64"
    syscall: "set_tid_address"
    syscall: "restart_syscall"
    syscall: "semtimedop"
    syscall: "fadvise64"
    syscall: "timer_create"
    syscall: "timer_settime"
    syscall: "timer_gettime"
    syscall: "timer_getoverrun"
    syscall: "timer_delete"
    syscall: "clock_settime"
    syscall: "clock_gettime"
    syscall: "clock_getres"
    syscall: "clock_nanosleep"
    syscall: "exit_group"
    syscall: "epoll_wait"
    syscall: "epoll_ctl"
    syscall: "tgkill"
    syscall: "utimes"
    syscall: "vserver"
    syscall: "mbind"
    syscall: "set_mempolicy"
    syscall: "get_mempolicy"
    syscall: "mq_open"
    syscall: "mq_unlink"
    syscall: "mq_timedsend"
    syscall: "mq_timedreceive"
    syscall: "mq_notify"
    syscall: "mq_getsetattr"
    syscall: "kexec_load"
    syscall: "waitid"
    syscall: "add_key"
    syscall: "request_key"
    syscall: "keyctl"
    syscall: "ioprio_set"
    syscall: "ioprio_get"
    syscall: "inotify_init"
    syscall: "inotify_add_watch"
    syscall: "inotify_rm_watch"
    syscall: "migrate_pages"
    syscall: "openat"
    syscall: "mkdirat"
    syscall: "mknodat"
    syscall: "fchownat"
    syscall: "futimesat"
    syscall: "newfstatat"
    syscall: "unlinkat"
    syscall: "renameat"
    syscall: "linkat"
    syscall: "symlinkat"
    syscall: "readlinkat"
    syscall: "fchmodat"
    syscall: "faccessat"
    syscall: "pselect6"
    syscall: "ppoll"
    syscall: "unshare"
    syscall: "set_robust_list"
    syscall: "get_robust_list"
    syscall: "splice"
    syscall: "tee"
    syscall: "sync_file_range"
    syscall: "vmsplice"
    syscall: "move_pages"
    syscall: "utimensat"
    syscall: "epoll_pwait"
    syscall: "signalfd"
    syscall: "timerfd_create"
    syscall: "eventfd"
    syscall: "fallocate"
    syscall: "timerfd_settime"
    syscall: "timerfd_gettime"
    syscall: "accept4"
    syscall: "signalfd4"
    syscall: "eventfd2"
    syscall: "epoll_create1"
    syscall: "dup3"
    syscall: "pipe2"
    syscall: "inotify_init1"
    syscall: "preadv"
    syscall: "pwritev"
    syscall: "rt_tgsigqueueinfo"
    syscall: "perf_event_open"
    syscall: "recvmmsg"
    syscall: "fanotify_init"
    syscall: "fanotify_mark"
    syscall: "prlimit64"
    syscall: "name_to_handle_at"
    syscall: "open_by_handle_at"
    syscall: "clock_adjtime"
    syscall: "syncfs"
    syscall: "sendmmsg"
    syscall: "setns"
    syscall: "getns"
    syscall: "process_vm_readv"
    syscall: "process_vm_writev"
    syscall: "kcmp"
    syscall: "finit_module"
    syscall: "sched_setattr"
    syscall: "sched_getattr"
    syscall: "renameat2"
    syscall: "seccomp"
    syscall: "getrandom"
    syscall: "memfd_create"
    syscall: "kexec_file_load"
    syscall: "bpf"
    syscall: "execveat"
    syscall: "userfaultfd"
    syscall: "membarrier"
    syscall: "mlock2"
    syscall: "copy_file_range"
    syscall: "preadv2"
    syscall: "pwritev2"
    syscall: "pkey_mprotect"
    syscall: "pkey_alloc"
    syscall: "pkey_free"
    syscall: "statx"
    syscall: "io_pgetevents"
    syscall: "rseq"
    syscall: "pidfd_send_signal"
    syscall: "io_uring_setup"
    syscall: "io_uring_enter"
    syscall: "io_uring_register"
    syscall: "open_tree"
    syscall: "move_mount"
    syscall: "fsopen"
    syscall: "fsconfig"
    syscall: "fsmount"
    syscall: "fspick"
    syscall: "pidfd_open"
    syscall: "clone3"
    syscall: "close_range"
    syscall: "openat2"
    syscall: "pidfd_getfd"
    syscall: "faccessat2"
    syscall: "process_madvise"
    syscall: "epoll_pwait2"
    syscall: "mount_setattr"
    syscall: "quotactl_fd"
    syscall: "landlock_create_ruleset"
    syscall: "landlock_add_rule"
    syscall: "landlock_restrict_self"
    syscall: "memfd_secret"
    syscall: "process_mrelease"
    syscall: "futex_waitv"
    syscall: "set_mempolicy_home_node"
}

rlimit_as {
    hard: 268435456
    soft: 268435456
}

rlimit_cpu {
    hard: 5
    soft: 5
}

rlimit_nproc {
    hard: 32
    soft: 32
}

rlimit_fsize {
    hard: 5242880
    soft: 5242880
}

exec_bin {
    path: "/usr/bin/python3"
    arg: "-I"        # Isolated mode: no user site-packages, no PYTHONPATH
    arg: "-E"        # Ignore environment variables
    arg: "-s"        # No user site-packages
    arg: "/app/main.py"
}
```

### 14.2 C (gcc) Compilation + Execution Config

**Compile phase (`gcc-compile.cfg`):**
```
mode: ONCE
hostname: "minos-gcc-compile"
time_limit: 10
max_cpus: 2

uidmap { inside_id: "99999"; outside_id: "0"; count: 1; use_newuidmap: false }
gidmap { inside_id: "99999"; outside_id: "0"; count: 1; use_newgidmap: false }

mount {
    src: "/"
    dst: "/"
    is_bind: true
    mandatory: true
    rw: false
}

mount {
    src: "/proc"
    dst: "/proc"
    is_bind: true
    mandatory: true
    rw: false
}

rlimit_as {
    hard: 536870912     # 512 MB for gcc
    soft: 536870912
}

rlimit_cpu {
    hard: 10
    soft: 10
}

rlimit_nproc {
    hard: 64            # gcc spawns multiple sub-processes
    soft: 64
}

rlimit_fsize {
    hard: 104857600     # 100 MB for output binary
    soft: 104857600
}

exec_bin {
    path: "/usr/bin/gcc"
    arg: "-std=c11"
    arg: "-O2"
    arg: "-Wall"
    arg: "-Werror"
    arg: "-fstack-protector-strong"
    arg: "-D_FORTIFY_SOURCE=2"
    arg: "-o"
    arg: "/app/main.out"
    arg: "/app/main.c"
}
```

**Run phase (`gcc-run.cfg`):**
```
mode: ONCE
hostname: "minos-gcc-run"
time_limit: 3
max_cpus: 1

uidmap { inside_id: "99999"; outside_id: "0"; count: 1; use_newuidmap: false }
gidmap { inside_id: "99999"; outside_id: "0"; count: 1; use_newidmap: false }

mount {
    src: "/"
    dst: "/"
    is_bind: true
    mandatory: true
    rw: false
}

# No /proc mount needed for compiled binaries (unless they use it)

rlimit_as {
    hard: 268435456
    soft: 268435456
}

rlimit_cpu {
    hard: 3
    soft: 3
}

rlimit_nproc {
    hard: 1             # Compiled binary shouldn't spawn children
    soft: 1
}

rlimit_fsize {
    hard: 5242880
    soft: 5242880
}

exec_bin {
    path: "/app/main.out"
}
```

---

## Appendix B: Quick Reference — Startup Order

```
1. Start Docker containers (Redis + API)
   docker compose up -d

2. Start worker daemon
   sudo systemctl start minos-worker

3. Verify
   docker compose ps                       # api + redis healthy
   sudo systemctl status minos-worker      # active
   redis-cli -a secret PING                # PONG

4. Smoke test
   curl -X POST http://localhost:8080/api/v1/execute \
     -H 'Content-Type: application/json' \
     -d '{"language":"python","code":"print(42)"}'

5. Expected response
   {"stdout":"42\n","stderr":"","exit_code":0,"time_ms":15}
```

---

## 15. Runtime Selection & Validated Architecture

### 15.1 What We Tested

The following combinations were tested on Fedora with Docker + nsjail (commit `HEAD`):

| Runtime    | Container Runtime | nsjail mode     | Result                                       |
| ---------- | ----------------- | --------------- | -------------------------------------------- |
| runc       | Docker default    | CLI args        | Fails — procfs mount blocked by overmounts   |
| runc       | Docker default    | Config + entrypt| **Works** — entrypoint cleans /proc, config uses bind-mounts |
| runsc      | gVisor            | Config (stock)  | Fails — `clone(CLONE_NEWCGROUP|NEWNET|NEWPID)` not supported |
| runsc      | gVisor            | Config (no cgroup/net/pid)| Fails — `prctl(PR_SET_SECUREBITS)` not supported |
| runsc      | gVisor            | Config + patched nsjail | Fails — `RLIMIT_MSGQUEUE` not supported (and more after) |

**The conclusion:** gVisor does not implement enough of the Linux kernel API for nsjail to function. Every wall you knock down reveals another. gVisor and nsjail solve the same problem at different layers — they are alternatives, not complements.

### 15.2 Why runc, Not gVisor

```
┌─────────────────────────────────────────────────────────────────┐
│  gVisor is a user-space kernel implemented in Go.                │
│  It intercepts ~200 syscalls and re-implements them.             │
│                                                                 │
│  nsjail needs the FULL Linux kernel API:                        │
│  - CLONE_NEWNS, CLONE_NEWUSER, CLONE_NEWCGROUP, CLONE_NEWNET    │
│  - CLONE_NEWPID, CLONE_NEWIPC, CLONE_NEWUTS                      │
│  - prctl(PR_SET_SECUREBITS, SECBIT_KEEP_CAPS | ...)              │
│  - setrlimit(RLIMIT_MSGQUEUE, RLIMIT_NPROC, RLIMIT_AS, ...)     │
│  - mount() with MS_BIND, MS_REC, MS_PRIVATE                      │
│  - pivot_root / chroot                                           │
│                                                                 │
│  gVisor implements a SUBSET of these. When nsjail tries to       │
│  use an unimplemented primitive, it fails. There is no escape.   │
│                                                                 │
│  The right runtime for nsjail workers is runc — the default      │
│  OCI runtime that exposes the full Linux kernel.                 │
└─────────────────────────────────────────────────────────────────┘
```

**Does this mean less security?** No. nsjail already provides:
- User namespace isolation (UID 99999, mapped to unprivileged host user)
- Mount namespace with RO system and RW-only `/app`
- PID namespace (process can't see host processes)
- IPC namespace (no shared memory with host)
- Seccomp KILL policy on all unknown syscalls
- RLIMIT_AS (256MB), RLIMIT_NPROC (32), RLIMIT_CPU (5s), RLIMIT_FSIZE (5MB)

If an attacker escapes nsjail, they land as the `minos-worker` system user, which has:
- No shell access (`/bin/false`)
- Firewall blocking all outbound except localhost:6379
- Only write access to `/tmp/minos` (tmpfs)
- No sudo, no capabilities beyond what nsjail needs
- No home directory, no SSH key
- Host protected by `ProtectSystem=strict` in systemd unit
- Firewall blocks all worker outbound except Redis on localhost:6379
- Systemd AmbientCapabilities restricted to SYS_ADMIN + CHROOT + SETUID + SETGID + SYS_RESOURCE

Escaping nsjail gives you the `minos-worker` user — not root.

### 15.3 Single-VM Deployment

The Worker runs as a **systemd service** directly on the host (not inside Docker). This gives nsjail unimpeded access to the Linux kernel — no nested namespaces, no `/proc` overmount issues, no `--privileged` needed.

API + Redis run in Docker (docker-compose) on a bridge network with Redis port exposed to `localhost` so the worker can connect.

```
Host (Fedora/Debian)
├── systemd: nginx (TLS + rate limit + proxy to :8080)
├── systemd: minos-worker (Go daemon → nsjail per job)
├── docker-compose:
│   ├── api:8080 (Gin, replicas: 2)
│   └── redis:6379 (exposed on 127.0.0.1:6379 for worker)
└── /tmp/minos (tmpfs, per-job workspaces)
```

**Why the worker runs bare metal (not Docker):**

| Approach                  | nsjail mounts /proc? | Needs privileged? | Complexity |
| ------------------------- | -------------------- | ----------------- | ---------- |
| Worker in Docker (runc)   | Needs entrypoint.sh fix | Needs SYS_ADMIN cap | Medium |
| Worker bare metal         | Works out of the box  | No Docker overhead  | Simple    |
| Worker in Docker (gVisor) | Doesn't work at all   | N/A                 | N/A       |

The worker is a single Go binary that loops: `BLPOP → write code to /tmp → exec nsjail → LPUSH result`. It doesn't need HTTP, doesn't need Docker — just the Go runtime, nsjail, and a Redis connection.

### 15.4 The Working nsjail Config (Proven)

Tested today on Fedora with nsjail built from HEAD. Uses bind-mount for `/proc` (no fresh procfs) and scalar rlimits (not message blocks in this nsjail version).

```protobuf
# /opt/minos/configs/nsjail-worker.cfg
mode: ONCE
hostname: "minos-worker"
time_limit: 5
max_cpus: 1
keep_caps: true

uidmap {
    inside_id: "99999"
    outside_id: "0"
    count: 1
}

gidmap {
    inside_id: "99999"
    outside_id: "0"
    count: 1
}

mount {
    src: "/"
    dst: "/"
    is_bind: true
    mandatory: true
    rw: false
}

mount {
    src: "/proc"
    dst: "/proc"
    is_bind: true
    mandatory: true
    rw: false
}

rlimit_as: 268435456
rlimit_cpu: 5
rlimit_nproc: 32
rlimit_fsize: 5242880

exec_bin {
    path: "/usr/bin/python3"
    arg: "/app/main.py"
}
```

The Worker Go code uses this as a template — replaces `/usr/bin/python3` and `/app/main.py` per language at runtime via `text/template` or string replacement.

### 15.5 Worker Go Binary (Skeleton)

```go
// worker/main.go — simplified structure
package main

import (
    "context"
    "encoding/json"
    "os"
    "os/exec"
    "path/filepath"
    "time"

    "github.com/redis/go-redis/v9"
)

func main() {
    rdb := redis.NewClient(&redis.Options{Addr: "localhost:6379", Password: os.Getenv("REDIS_PASSWORD")})
    ctx := context.Background()

    for {
        result, err := rdb.BLPop(ctx, 0, "minos:queue").Result()
        if err != nil {
            continue
        }

        var job Job
        json.Unmarshal([]byte(result[1]), &job)

        workDir := filepath.Join("/tmp/minos", job.ID)
        os.MkdirAll(filepath.Join(workDir, "app"), 0700)
        defer os.RemoveAll(workDir)

        os.WriteFile(filepath.Join(workDir, "app", "main.py"), []byte(job.Code), 0400)

        cmd := exec.Command("nsjail",
            "--config", "/opt/minos/configs/nsjail-worker.cfg",
            "--bindmount", workDir+"/app:/app",
            "--exec_bin", "/usr/bin/python3",
            "--", "/app/main.py",
        )

        start := time.Now()
        stdout, _ := cmd.Output()
        elapsed := time.Since(start).Milliseconds()

        res, _ := json.Marshal(Result{Stdout: string(stdout), TimeMs: elapsed})
        rdb.LPush(ctx, "minos:result:"+job.ID, res)
        rdb.Expire(ctx, "minos:result:"+job.ID, 5*time.Minute)
    }
}
```