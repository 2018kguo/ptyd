//go:build loadtest

package loadtest

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestScenarioScriptsPropagateFailure(t *testing.T) {
	for _, name := range []string{"load.sh", "load-ab.sh"} {
		for _, fail := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/fail=%t", name, fail), func(t *testing.T) {
				dir := t.TempDir()
				writeLoadFixture(t, filepath.Join(dir, "tools/go"), `#!/bin/sh
while [ "$#" -gt 0 ]; do
 if [ "$1" = -o ]; then shift; binary=$1; fi
 shift
done
if ! cmp -s internal/loadtest/current.go "$LOAD_TEST_HARNESS" || [ -e internal/loadtest/old-only.go ]; then
 echo 'load harness differs between builds' >&2
 exit 81
fi
cat >"$binary" <<'RUN'
#!/bin/sh
printf '%s\n' "$PTYD_LOAD_LABEL" >>"$LOAD_TEST_RUNS"
if [ "$LOAD_TEST_FAIL" = 1 ]; then
 echo 'intentional scenario failure' >&2
 exit 42
fi
printf '{}\n' >>"$PTYD_LOAD_OUT"
RUN
chmod +x "$binary"
`)
				writeLoadFixture(t, filepath.Join(dir, "tools/git"), `#!/bin/sh
if [ "$1" = worktree ] && [ "$2" = add ]; then
 mkdir -p "$5/internal/loadtest"
 echo old >"$5/internal/loadtest/old-only.go"
fi
`)
				harness := filepath.Join(dir, "internal/loadtest/current.go")
				writeLoadFixture(t, harness, "current load harness\n")
				script, err := os.ReadFile(filepath.Join("../../bench", name))
				if err != nil {
					t.Fatal(err)
				}
				path := filepath.Join(dir, "bench", name)
				writeLoadFixture(t, path, string(script))
				out, runs := filepath.Join(dir, "out.jsonl"), filepath.Join(dir, "runs.txt")
				arg := out
				if name == "load-ab.sh" {
					arg = "HEAD"
				}
				cmd := exec.Command("sh", path, arg)
				failure := "0"
				if fail {
					failure = "1"
				}
				cmd.Env = append(os.Environ(), "PATH="+filepath.Join(dir, "tools")+":"+os.Getenv("PATH"),
					"LOAD_TEST_RUNS="+runs, "LOAD_TEST_FAIL="+failure, "LOAD_TEST_HARNESS="+harness, "OUT="+out, "ROUNDS=1")
				output, err := cmd.CombinedOutput()
				if fail {
					if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 42 || !strings.Contains(string(output), "intentional scenario failure") {
						t.Fatalf("scenario failure was hidden: error=%v output=%s", err, output)
					}
				} else if err != nil {
					t.Fatalf("healthy scenarios failed: %v\n%s", err, output)
				}
				data, err := os.ReadFile(runs)
				if err != nil {
					t.Fatal(err)
				}
				want := 4
				if fail {
					want = 1
				}
				if got := strings.Count(string(data), "\n"); got != want {
					t.Fatalf("ran %d scenarios, want %d", got, want)
				}
			})
		}
	}
}

func writeLoadFixture(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o700); err != nil {
		t.Fatal(err)
	}
}
