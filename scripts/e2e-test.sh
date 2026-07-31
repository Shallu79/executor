#!/usr/bin/env bash
# End-to-end smoke test for the worker stack.
#
# Prereq: docker compose up -d --build
# Pushes jobs straight into minos:queue (the way the API gateway will) and
# prints each result from minos:result:{id}.
set -euo pipefail
cd "$(dirname "$0")/.."

CLI=(docker compose exec -T redis redis-cli)

submit() { # <id> <payload-file>
    local id="$1" file="$2"
    "${CLI[@]}" -x RPUSH minos:queue < "$file" > /dev/null
    echo "== $id =="
    "${CLI[@]}" BLPOP "minos:result:$id" 30 | tail -1
    echo
}

TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT

# 1. Python hello world → exit 0, stdout "42\n"
cat > "$TMP/t1.json" <<'EOF'
{"id":"t1-python-hello","language":"python","code":"print(42)"}
EOF

# 2. Python infinite loop → nsjail time_limit (5s) kills it → 137 + timeout msg
cat > "$TMP/t2.json" <<'EOF'
{"id":"t2-python-timeout","language":"python","code":"while True: pass"}
EOF

# 3. Python non-zero exit → exit code must propagate (3)
cat > "$TMP/t3.json" <<'EOF'
{"id":"t3-python-exitcode","language":"python","code":"import sys; sys.exit(3)"}
EOF

# 4. Python exception → exit 1, stderr has the traceback
cat > "$TMP/t4.json" <<'EOF'
{"id":"t4-python-stderr","language":"python","code":"raise ValueError(\"boom\")"}
EOF

# 5. Python with stdin
cat > "$TMP/t5.json" <<'EOF'
{"id":"t5-python-stdin","language":"python","code":"import sys; print(\"got:\", sys.stdin.read().strip())","stdin":"hello-stdin"}
EOF

# 6. C two-phase: compile + run → stdout from the compiled binary
cat > "$TMP/t6.json" <<'EOF'
{"id":"t6-c-run","language":"c","code":"#include <stdio.h>\nint main(void){printf(\"hi from c\\n\");return 0;}"}
EOF

# 7. C compile error → "Compilation error:" prefix, no run phase
cat > "$TMP/t7.json" <<'EOF'
{"id":"t7-c-compile-err","language":"c","code":"int main( { return 0; }"}
EOF

# 8. Output truncation: 100KB printed → stdout capped at 65536, truncated=true
cat > "$TMP/t8.json" <<'EOF'
{"id":"t8-python-truncate","language":"python","code":"print(\"x\"*100000)"}
EOF

# 9. JavaScript (node) hello
cat > "$TMP/t9.json" <<'EOF'
{"id":"t9-node-hello","language":"javascript","code":"console.log(\"hi from node\")"}
EOF

# 10. Go two-phase: go run main.go
cat > "$TMP/t10.json" <<'EOF'
{"id":"t10-go-run","language":"go","code":"package main\nimport \"fmt\"\nfunc main() { fmt.Println(\"hi from go\") }"}
EOF

# 11. Java two-phase: javac + java Main
cat > "$TMP/t11.json" <<'EOF'
{"id":"t11-java-run","language":"java","code":"public class Main { public static void main(String[] a) { System.out.println(\"hi from java\"); } }"}
EOF

# 12. C++ two-phase: g++ + run
cat > "$TMP/t12.json" <<'EOF'
{"id":"t12-cpp-run","language":"cpp","code":"#include <iostream>\nint main(){ std::cout << \"hi from c++\\n\"; return 0; }"}
EOF

for t in t1 t2 t3 t4 t5 t6 t7 t8 t9 t10 t11 t12; do
    submit "$(sed -n 's/.*"id":"\([^"]*\)".*/\1/p' "$TMP/$t.json")" "$TMP/$t.json"
done

echo "All jobs returned. Verify:"
echo "  t1: exit_code 0, stdout 42"
echo "  t2: exit_code 137, ~5000ms, stderr mentions timeout"
echo "  t3: exit_code 3"
echo "  t4: exit_code 1, stderr has ValueError"
echo "  t5: stdout has hello-stdin"
echo "  t6: exit_code 0, stdout hi from c"
echo "  t7: exit_code != 0, stderr starts with Compilation error"
echo "  t8: stdout length 65536, truncated true"
echo "  t9: exit_code 0, stdout hi from node"
echo "  t10: exit_code 0, stdout hi from go"
echo "  t11: exit_code 0, stdout hi from java"
echo "  t12: exit_code 0, stdout hi from c++"
