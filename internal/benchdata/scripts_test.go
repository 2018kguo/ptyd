package benchdata

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestBenchmarkScriptsRejectFailedSamples(t *testing.T) {
	for _, name := range []string{"run.sh", "ab.sh", "pgo.sh", "load.sh", "load-ab.sh"} {
		for _, failure := range []string{"", "sample", "build"} {
			t.Run(name+"/"+failure, func(t *testing.T) {
				dir := benchmarkScriptFixture(t, name)
				out := filepath.Join(dir, "result")
				if name == "pgo.sh" {
					writeScriptFixture(t, out, "original profile\n")
				}
				args := []string{filepath.Join(dir, "bench", name)}
				if name == "ab.sh" {
					args = append(args, "HEAD", "./internal/terminal ./internal/api", "Fixture")
				} else if name == "load-ab.sh" {
					args = append(args, "HEAD")
				} else if name == "load.sh" {
					args = append(args, out)
				}
				cmd := exec.Command("sh", args...)
				cmd.Env = append(os.Environ(), "PATH="+filepath.Join(dir, "tools")+":"+os.Getenv("PATH"),
					"SCRIPT_TEST_DIR="+dir, "SCRIPT_TEST_FAILURE="+failure, "SCRIPT_TEST_NAME="+name,
					"OUT="+out, "COUNT=2", "ROUNDS=2", "BENCH=Fixture", "BENCHTIME=100x", "PTYD_LOAD_PROFILE=")
				output, err := cmd.CombinedOutput()
				if failure != "" {
					want := 42
					if failure == "build" {
						want = 43
					}
					if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != want || !strings.Contains(string(output), "intentional "+failure+" failure") {
						t.Fatalf("failure was hidden: error=%v output=%s", err, output)
					}
					if _, err := os.Stat(filepath.Join(dir, "compared")); !os.IsNotExist(err) {
						t.Fatal("compared an incomplete run")
					}
					if name == "pgo.sh" {
						if data, err := os.ReadFile(out); err != nil || string(data) != "original profile\n" {
							t.Fatal("failed capture replaced the existing profile")
						}
					} else if name == "ab.sh" {
						if _, err := os.Stat(out); !os.IsNotExist(err) {
							t.Fatal("published an incomplete comparison")
						}
					}
				} else if err != nil {
					t.Fatalf("healthy script failed: %v\n%s", err, output)
				} else if name == "ab.sh" {
					if _, err := os.Stat(filepath.Join(dir, "compared")); err != nil {
						t.Fatal("healthy samples were not compared")
					}
				} else if name == "pgo.sh" {
					if data, err := os.ReadFile(out); err != nil || string(data) != "merged profile\n" {
						t.Fatal("healthy capture did not replace the profile")
					}
				}
				var want []string
				switch name {
				case "run.sh":
					want = []string{"terminal.test", "api.test"}
				case "ab.sh":
					want = []string{"old-terminal.test", "new-terminal.test", "old-api.test", "new-api.test", "new-terminal.test", "old-terminal.test", "new-api.test", "old-api.test"}
				case "pgo.sh":
					want = []string{"load.test", "load.test"}
				case "load-ab.sh":
					want = []string{"a:agents", "a:flat-out", "b:agents", "b:flat-out", "b:agents", "b:flat-out", "a:agents", "a:flat-out"}
				case "load.sh":
					want = []string{"agents", "flat-out", "steady-disk", "bursty-disk"}
				}
				if failure == "sample" {
					want = want[:1]
				} else if failure == "build" {
					want = nil
				}
				runs, err := os.ReadFile(filepath.Join(dir, "runs"))
				if len(want) == 0 && os.IsNotExist(err) {
					return
				}
				if err != nil || strings.TrimSpace(string(runs)) != strings.Join(want, "\n") {
					t.Fatalf("run order = %q, want %q (error %v)", runs, want, err)
				}
			})
		}
	}
}

func TestLoadScriptsCreateOutputDirectories(t *testing.T) {
	for _, name := range []string{"load.sh", "load-ab.sh"} {
		for _, mode := range []string{"default", "custom"} {
			t.Run(name+"/"+mode, func(t *testing.T) {
				dir := benchmarkScriptFixture(t, name)
				file := "load.jsonl"
				if name == "load-ab.sh" {
					file = "load-ab.jsonl"
				}
				out, setting := filepath.Join(dir, ".bench", file), ""
				args := []string{filepath.Join(dir, "bench", name)}
				if mode == "custom" {
					out = filepath.Join(dir, "new", "nested", file)
					setting = out
				}
				if name == "load-ab.sh" {
					args = append(args, "HEAD")
				} else if mode == "custom" {
					args = append(args, out)
				}
				cmd := exec.Command("sh", args...)
				cmd.Env = append(os.Environ(), "PATH="+filepath.Join(dir, "tools")+":"+os.Getenv("PATH"),
					"SCRIPT_TEST_DIR="+dir, "SCRIPT_TEST_FAILURE=", "SCRIPT_TEST_NAME="+name,
					"OUT="+setting, "ROUNDS=1", "PTYD_LOAD_PROFILE=")
				if output, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("script failed: %v\n%s", err, output)
				}
				if data, err := os.ReadFile(out); err != nil || strings.Count(string(data), "\n") != 4 {
					t.Fatalf("output was not written at %s: %q (error %v)", out, data, err)
				}
			})
		}
	}
}

func benchmarkScriptFixture(t *testing.T, name string) string {
	t.Helper()
	dir := t.TempDir()
	script, err := os.ReadFile(filepath.Join("../../bench", name))
	if err != nil {
		t.Fatal(err)
	}
	writeScriptFixture(t, filepath.Join(dir, "bench", name), string(script))
	for _, pkg := range []string{"terminal", "api"} {
		writeScriptFixture(t, filepath.Join(dir, "internal", pkg, "fixture_bench_test.go"), "same benchmark\n")
	}
	writeScriptFixture(t, filepath.Join(dir, "internal/loadtest/fixture.go"), "same load harness\n")
	writeScriptFixture(t, filepath.Join(dir, "tools/git"), `#!/bin/sh
if [ "$1" = worktree ] && [ "$2" = add ]; then
 mkdir -p "$5/internal/terminal" "$5/internal/api" "$5/internal/loadtest"
 echo old >"$5/internal/loadtest/old-only.go"
fi
`)
	writeScriptFixture(t, filepath.Join(dir, "tools/go"), `#!/bin/sh
set -eu
case "$1" in
test)
 if [ "$SCRIPT_TEST_FAILURE" = build ]; then echo 'intentional build failure' >&2; exit 43; fi
 if [ "$SCRIPT_TEST_NAME" = ab.sh ]; then
  for arg in "$@"; do
   case "$arg" in ./internal/*) cmp "$arg/fixture_bench_test.go" "$SCRIPT_TEST_DIR/$arg/fixture_bench_test.go";; esac
  done
 fi
 if [ "$SCRIPT_TEST_NAME" = load-ab.sh ]; then
  cmp internal/loadtest/fixture.go "$SCRIPT_TEST_DIR/internal/loadtest/fixture.go"
  [ ! -e internal/loadtest/old-only.go ]
 fi
 while [ "$#" -gt 0 ]; do
  if [ "$1" = -o ]; then shift; binary=$1; fi
  shift
 done
 cat >"$binary" <<'RUN'
#!/bin/sh
set -eu
label=$(basename "$0")
case "$SCRIPT_TEST_NAME" in
 run.sh|ab.sh)
  found=0
  while [ "$#" -gt 0 ]; do
   if [ "$1" = -test.benchtime ]; then shift; [ "$1" = 100x ]; found=1; fi
   shift
  done
  [ "$found" = 1 ]
  ;;
esac
case "$SCRIPT_TEST_NAME" in load.sh|load-ab.sh) label=$PTYD_LOAD_LABEL;; esac
printf '%s\n' "$label" >>"$SCRIPT_TEST_DIR/runs"
printf 'BenchmarkFixture-1 1 10 ns/op\n'
echo 'fixture stderr diagnostic' >&2
if [ "$SCRIPT_TEST_FAILURE" = sample ]; then echo 'intentional sample failure' >&2; exit 42; fi
if [ -n "${PTYD_LOAD_PROFILE:-}" ]; then mkdir -p "$PTYD_LOAD_PROFILE"; : >"$PTYD_LOAD_PROFILE/cpu.pprof"; fi
case "$SCRIPT_TEST_NAME" in load.sh|load-ab.sh) printf '{}\n' >>"$PTYD_LOAD_OUT";; esac
RUN
 chmod +x "$binary"
 ;;
run)
 case "$2" in golang.org/x/perf/cmd/benchstat@v*) ;; *) echo 'benchstat must be pinned' >&2; exit 80;; esac
 for arg in "$@"; do
  case "$arg" in old=*|new=*) grep -q '^BenchmarkFixture-' "${arg#*=}";; esac
 done
 : >"$SCRIPT_TEST_DIR/compared"
 ;;
tool)
 shift 3
 for profile in "$@"; do [ -f "$profile" ]; done
 echo 'merged profile'
 ;;
*) exit 81;;
esac
`)
	return dir
}

func writeScriptFixture(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o700); err != nil {
		t.Fatal(err)
	}
}
