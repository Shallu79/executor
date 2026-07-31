// Package languages defines the supported-language registry shared by the
// API gateway (validation) and the worker (execution). A language is only
// usable if its runtime is installed in the worker image and it has an
// entry here — the registry is the allowlist.
package languages

// Language describes how a submission in one programming language is
// executed inside nsjail. Compiled languages additionally fill the
// Compile* fields and run in two phases (ARCHITECTURE.md section 7.2).
type Language struct {
	Name       string   // registry key, e.g. "python"
	SourceFile string   // file name written into the workspace, e.g. "main.py"
	ExecPath   string   // binary executed inside the jail (run phase)
	ExecArgs   []string // args for the run phase (source path included)
	Env        []string // extra "KEY=VAL" for the run phase

	// Compile phase — empty CompilePath means interpreted (single phase).
	CompilePath      string
	CompileArgs      []string
	CompileEnv       []string // extra "KEY=VAL" for compilation (e.g. TMPDIR=/app)
	CompileTimeLimit int      // seconds
	CompileMemory    uint64   // bytes (rlimit_as)
	CompileMaxProcs  uint64   // gcc/g++/javac spawn sub-processes
	CompileMaxFSize  uint64   // bytes (rlimit_fsize) — room for the output binary

	// Run-phase limits.
	TimeLimit   int    // seconds — drives nsjail time_limit, rlimit_cpu, ctx timeout
	Memory      uint64 // bytes (rlimit_as)
	MaxProcs    uint64 // rlimit_nproc
	MaxFileSize uint64 // bytes (rlimit_fsize)
}

const (
	mib = 1 << 20
)

// DefaultLanguages is the built-in registry. Every runtime listed here must
// be installed in the worker image (see deploy/docker/Dockerfile.worker);
// adding a language is a matter of installing its runtime and adding one
// entry here.
func DefaultLanguages() map[string]Language {
	python := Language{
		Name:        "python",
		SourceFile:  "main.py",
		ExecPath:    "/usr/bin/python3",
		ExecArgs:    []string{"-I", "-E", "-s", "/app/main.py"},
		TimeLimit:   5,
		Memory:      256 * mib,
		MaxProcs:    32,
		MaxFileSize: 5 * mib,
	}

	bash := Language{
		Name:        "bash",
		SourceFile:  "main.sh",
		ExecPath:    "/usr/bin/bash",
		ExecArgs:    []string{"/app/main.sh"},
		TimeLimit:   3,
		Memory:      64 * mib,
		MaxProcs:    32,
		MaxFileSize: 5 * mib,
	}

	node := Language{
		Name:        "javascript",
		SourceFile:  "main.js",
		ExecPath:    "/usr/bin/node",
		ExecArgs:    []string{"/app/main.js"},
		TimeLimit:   5,
		Memory:      256 * mib,
		MaxProcs:    32,
		MaxFileSize: 5 * mib,
	}

	goLang := Language{
		Name:       "go",
		SourceFile: "main.go",
		ExecPath:   "/usr/bin/go",
		ExecArgs:   []string{"run", "main.go"},
		Env: []string{
			"GOCACHE=/app/.cache", // / is RO — the toolchain must cache inside /app
			"GOTMPDIR=/app",
			"HOME=/app",
			"GOTOOLCHAIN=local", // never try to download a toolchain (no network)
		},
		TimeLimit:   5,
		Memory:      512 * mib, // the go toolchain compiles on the fly
		MaxProcs:    64,        // go spawns helper processes during build
		MaxFileSize: 100 * mib, // build cache + binary
	}

	c := Language{
		Name:             "c",
		SourceFile:       "main.c",
		CompilePath:      "/usr/bin/gcc",
		CompileArgs:      []string{"-std=c11", "-O2", "-Wall", "-fstack-protector-strong", "-o", "/app/main.out", "/app/main.c"},
		CompileEnv:       []string{"TMPDIR=/app"}, // / is read-only; gcc needs a tmp dir
		CompileTimeLimit: 10,
		CompileMemory:    512 * mib,
		CompileMaxProcs:  64,
		CompileMaxFSize:  100 * mib,
		ExecPath:         "/app/main.out",
		TimeLimit:        3,
		Memory:           256 * mib,
		MaxProcs:         1, // compiled binaries shouldn't spawn children
		MaxFileSize:      5 * mib,
	}

	cpp := Language{
		Name:             "cpp",
		SourceFile:       "main.cpp",
		CompilePath:      "/usr/bin/g++",
		CompileArgs:      []string{"-std=c++17", "-O2", "-Wall", "-fstack-protector-strong", "-o", "/app/main.out", "/app/main.cpp"},
		CompileEnv:       []string{"TMPDIR=/app"},
		CompileTimeLimit: 10,
		CompileMemory:    512 * mib,
		CompileMaxProcs:  64,
		CompileMaxFSize:  100 * mib,
		ExecPath:         "/app/main.out",
		TimeLimit:        3,
		Memory:           256 * mib,
		MaxProcs:         1,
		MaxFileSize:      5 * mib,
	}

	java := Language{
		Name:             "java",
		SourceFile:       "Main.java",
		CompilePath:      "/usr/bin/javac",
		CompileArgs:      []string{"-d", "/app", "/app/Main.java"},
		CompileEnv:       []string{"TMPDIR=/app"},
		CompileTimeLimit: 10,
		CompileMemory:    512 * mib,
		CompileMaxProcs:  64,
		CompileMaxFSize:  100 * mib,
		ExecPath:         "/usr/bin/java",
		ExecArgs:         []string{"-Xmx256m", "-Djava.io.tmpdir=/app", "-cp", "/app", "Main"},
		TimeLimit:        5,
		Memory:           512 * mib, // JVM reserves more virtual address space than it uses
		MaxProcs:         32,
		MaxFileSize:      5 * mib,
	}

	return map[string]Language{
		python.Name: python,
		bash.Name:   bash,
		node.Name:   node,
		goLang.Name: goLang,
		c.Name:      c,
		cpp.Name:    cpp,
		java.Name:   java,
	}
}

// IsSupported reports whether name is a registered language.
func IsSupported(name string) bool {
	_, ok := DefaultLanguages()[name]
	return ok
}
