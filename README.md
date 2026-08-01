# Minos · Exec Engine

A secure, multi-language code execution engine. Users submit code (or an API key submits it for them), the job is queued, executed in a hardened sandbox, and stdout / stderr / exit code / timing are returned.

- **Sandboxing:** every job runs inside an [nsjail](https://github.com/google/nsjail) sandbox: mount namespaces, PID namespaces, seccomp filters, resource limits and no network.
- **Async by design:** API → Redis queue → worker pool → Redis results. The API stays responsive while jobs execute.
- **API keys:** scoped keys for humans and CI pipelines. The server only stores key hashes.
- **Languages:** Python, JavaScript/Node, Go, C, C++, Java and Bash, with per-language time / memory / process / file-size limits.

---

## What it solves

Running untrusted code on your own machine is dangerous. A single `fork bomb`, `rm -rf /`, or network exfiltration attempt is enough to compromise a host or leak data. Minos solves the problem of **"how do I run someone else's code safely, at scale, and measure it"**:

1. **Isolation**: each submission executes in an isolated sandbox with no access to the host, other users' files, or the network.
2. **Resource control**: hard CPU, memory, process-count and output limits enforced by the kernel, so a runaway job cannot take the host down.
3. **Async workload**: jobs are pushed to a Redis queue and consumed by a pool of workers. The submission API is never blocked by slow code.
4. **Clean lifecycle**: every job gets a private workspace that is created and destroyed around it; nothing persists between runs.

---

## Sandbox architecture: Docker containers vs. isolate-style sandboxes

Minos isolates jobs at the **process/namespace level** (nsjail, the same family as the `isolate` tool used by online judges) rather than by spawning a Docker container per job. That choice is deliberate.

| Dimension | Docker container per job | nsjail / isolate |
| --- | --- | --- |
| **Spawn cost** | High: daemon API, image/rootfs, overlayfs, cgroup setup | Low: a single `clone` + namespace setup |
| **Memory footprint** | Full runtime + image layers per container | A few MB per sandbox |
| **Throughput** | Container lifecycle dominates | Sub-millisecond-to-low-ms sandbox setup |
| **Network** | On by default (needs explicit `--network none`) | Off unless explicitly enabled |
| **Filesystem** | Full rootfs, image pulls, disk pressure | Bind-mount one read-only tree + a writable scratch dir |
| **Failure mode** | Docker daemon is a single point of failure | No daemon; sandbox spawn is just an exec |

The worker starts a job by rendering a per-phase nsjail config (`compile`, `run`), binding only the job's `app/` directory into the jail as `/app`, then executing the phase with layered timeouts:

1. nsjail's kernel-enforced `time_limit` / `rlimit_cpu`
2. a Go context deadline as a fallback
3. `WaitDelay` to cap blocking on inherited pipes

Results carry the measured wall-clock time (`time_ms`), which is what the benchmark below reports.

### Benchmark: Spawn + Execute Time (ms)

This benchmark compares the total execution time (spawn + execute, in milliseconds) between **Docker containers** and **nsjail/isolate** environments across architectures.

---

#### ⚡ Nsjail
The following metrics reflect the spawn and execution times when using `isolate`:

<img width="720" alt="Isolate benchmark metrics" src="https://github.com/user-attachments/assets/401b33f2-68bb-4d57-9e77-07f2158ac148" />

#### 🐳 Docker Spawns
The following metrics reflect the spawn and execution times when using standard `docker` containers:

<img width="670" alt="Docker benchmark metrics" src="https://github.com/user-attachments/assets/08569d8e-8be1-45df-ba76-5e2ec5af50e8" />


---

## Scalability on AWS

Minos is built to scale horizontally and to stay secure while it does. The two services that need capacity (the API and the workers) scale independently.

### Deployment topology

```mermaid
flowchart LR
    I[("Internet")]

    subgraph VPC["VPC"]
        subgraph Pub["Public subnet"]
            ALB["API / ALB<br/>(frontend)"]
        end

        subgraph Priv["Private subnets"]
            W1["Worker ASG<br/>(nsjail pool)"]
            W2["Worker ASG<br/>(nsjail pool)"]
        end

        R[("ElastiCache<br/>Redis")]
        PG[("RDS<br/>Postgres")]
    end

    I -->|HTTPS 443| ALB
    ALB --> R
    ALB --> PG
    W1 --> R
    W2 --> R

    classDef nout fill:#7f1d1d,color:#fff
    class W1,W2 nout
```

### Workers are isolated by design

- **Private subnets only**: worker instances have **no public IPs** and **no route to the internet**. A compromised sandbox has nothing to phone home to.
- **No egress**: since `NSJAIL_CLONE_NEWNET` jails drop network namespaces anyway, the worker host itself sits on a private subnet with an outbound route that is simply absent; there is no NAT gateway for the workers at all.
- **State only through Redis**: workers talk to ElastiCache (Redis) inside the VPC and nothing else. The queue and results channel is the only interface a worker needs.

### Scaling

- **API tier:** scale out behind an ALB in public subnets; it is stateless apart from the JWT secret and talks to Redis/Postgres over the internal network.
- **Worker tier:** scale out with an Auto Scaling Group (CPU / queue-depth based). More workers means more concurrent jobs; each worker runs a configurable goroutine pool (`CONCURRENCY`).
- **Queue as the buffer:** Redis `BLPOP` keeps idle workers parked, so spiky traffic is absorbed by the queue instead of over-provisioned instances.
- **Storage:** RDS + ElastiCache are managed and scale independently; submission retention is enforced by a cleanup job (`SUBMISSION_RETENTION_DAYS`).

### Security hardening checklist

- Security groups allow API→Redis/Postgres on the internal ports only; workers accept traffic from Redis only.
- Worker instances run with the Docker daemon **not installed**: the sandbox is nsjail, so there is no container breakout surface to patch or a daemon to exploit.
- Job workspaces are `0700`, created and removed per job; `app/` is the only writable mount.
- Output is bounded (`MaxOutputSize`) and retained per retention policy.

---

## Development

### Prerequisites

- Docker + Docker Compose
- Go 1.25+
- [bun](https://bun.sh) (for the web frontend)

### 1. Configure environment

```bash
cp .env.example .env
# Edit .env if you need different ports/secrets.
```

The compose stack (Redis, Postgres, worker, API) reads these values from `.env`.

### 2. Run the full stack with Docker

```bash
docker compose up -d --build
# or, via the Makefile:
make up
```

This brings up:

| Service  | Description                        | Reachable at |
| ---      | ---                                | ---          |
| `redis`  | Job queue + result channel         | internal only |
| `postgres` | Submission history, accounts, keys | internal only |
| `worker` | nsjail executor + worker pool      | internal only |
| `api`    | REST gateway                       | `http://127.0.0.1:8080` |
| `web`    | Built React frontend (Caddy)       | `http://127.0.0.1:3000` |

Only the API (`8080`) and the web UI (`3000`) are exposed to the host; Redis and Postgres stay on the internal bridge network. Caddy inside the `web` container just serves the static build (with SPA fallback). The frontend calls the API directly and the API's CORS middleware (`Access-Control-Allow-Origin: *`) allows it, so no reverse proxy is needed.

By default the frontend is built with `VITE_API_BASE=http://127.0.0.1:8080` (the host-exposed API). To point it at a different API, export `VITE_API_BASE` before `docker compose up`:

```bash
VITE_API_BASE=https://api.example.com docker compose up -d --build web
```

The base URL is baked in at build time and is not user-configurable at runtime.

### 3. Verify it's up

```bash
curl http://127.0.0.1:8080/healthz   # liveness
curl http://127.0.0.1:8080/readyz    # readiness
```

### 4. Try a job

```bash
# Push a python job straight onto the queue and read the result:
make smoke

# Full end-to-end suite (hello, timeout, exit codes, stderr, stdin, C, truncation):
make e2e

# API smoke test (signup -> execute -> result, plus auth checks):
make api-smoke
```

### 5. Run binaries directly (no containers for worker/API)

For faster iteration you can build and run the API and worker as plain processes against Docker-run Redis/Postgres:

```bash
# Start just the data stores:
docker compose up -d redis postgres

# Build the binaries:
make build

# Worker (needs nsjail on the host and TMP_DIR writable):
TMP_DIR=/tmp/minos NSJAIL_BIN=nsjail REDIS_ADDR=localhost:6379 ./bin/minos-worker

# API (needs DATABASE_URL pointing at the local postgres):
DATABASE_URL=postgres://minos:minos-secret@localhost:5432/minos \
JWT_SECRET=dev-jwt-secret-change-me \
REDIS_ADDR=localhost:6379 ./bin/minos-api
```

Tests, vet and fmt are also available:

```bash
make test
make vet
make fmt
```

### 6. Frontend (web/)

```bash
cd web
bun install
bun run dev      # Vite dev server, serves src/ against the local API
```

Lint and production build:

```bash
bun run lint
bun run build
```

### 7. Tear down

```bash
docker compose down -v   # -v also wipes the postgres volume
make logs                # tail worker logs
```

## Layout

- `cmd/api` - REST gateway: auth, API keys, submissions, job submit/poll
- `cmd/worker`: nsjail executor + worker pool
- `internal/`: api, auth, config, languages, models, redisq, store, worker
- `deploy/docker`: container images for the API and worker
- `web/`: React frontend (CodeMirror IDE + account pages)
- `ARCHITECTURE.md`: deep dive into the design decisions
