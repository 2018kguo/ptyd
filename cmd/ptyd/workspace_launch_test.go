package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/2018kguo/phosphor"
)

func TestWorkspaceAgentLaunchAccess(t *testing.T) {
	bin := t.TempDir()
	record := filepath.Join(t.TempDir(), "arguments")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$PTYD_TEST_ARGS\"\nprintf 'FAKE-%s-READY\\n' \"${0##*/}\"\nexec cat\n"
	for _, command := range []string{"codex", "claude"} {
		if err := os.WriteFile(filepath.Join(bin, command), []byte(script), 0700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("PTYD_TEST_ARGS", record)
	d := startDaemon(t, freeAddr(t), "--persist-dir", t.TempDir())
	if _, err, code := d.cli(nil, "new", "-d", "access"); code != 0 {
		t.Fatal(err)
	}
	w := d.open(120, 32, "attach", "access")
	w.waitText("This group is ready.")
	cases := []struct {
		key, command, flag string
		toggle, full       bool
	}{
		{"c", "codex", "--dangerously-bypass-approvals-and-sandbox", false, true},
		{"a", "claude", "--dangerously-skip-permissions", true, false},
		{"c", "codex", "--dangerously-bypass-approvals-and-sandbox", false, false},
		{"a", "claude", "--dangerously-skip-permissions", true, true},
	}
	for _, tc := range cases {
		w.keys("n")
		w.waitText("Start a terminal")
		if tc.toggle {
			w.keys("f")
		}
		label := "Access: Full (YOLO)"
		if !tc.full {
			label = "Access: CLI settings"
		}
		w.waitText(label)
		w.keys(tc.key)
		w.waitText("FAKE-" + tc.command + "-READY")
		raw, err := os.ReadFile(record)
		if err != nil {
			t.Fatal(err)
		}
		args := strings.Split(strings.TrimSpace(string(raw)), "\n")
		if got := slices.Contains(args, tc.flag); got != tc.full {
			t.Fatalf("%s full=%v received arguments %q", tc.command, tc.full, args)
		}
		// Persistence enables the real restore adapter, including its hook flags.
		if !slices.Contains(args, "--settings") && !slices.Contains(args, "-c") {
			t.Fatal("agent restore hooks were lost")
		}
		w.keys("jk")
		w.waitText("Returned to your workspace.")
	}
	w.keys("n")
	w.waitText("Access: Full (YOLO)")
	w.keys("f")
	w.waitText("Access: CLI settings")
	w.keys("jk")
	w.waitNoText("Start a terminal")
	w.keys("q")
	if code := w.waitExit(); code != 0 {
		t.Fatal(code)
	}
	again := d.open(120, 32, "attach", "access")
	again.waitText("TERMINALS")
	again.keys("n")
	again.waitText("Access: Full (YOLO)")
	again.keys("jk")
	again.waitNoText("Start a terminal")
	again.keys("q")
	if code := again.waitExit(); code != 0 {
		t.Fatal(code)
	}
}

func TestWorkspaceAgentAccessFitsSmallWindows(t *testing.T) {
	for _, size := range [][2]int{{40, 16}, {80, 24}, {120, 30}} {
		for _, access := range []workspaceAgentAccess{workspaceFullAccess, workspaceCLISettings} {
			view := workspaceView{mode: wsLaunch, agentAccess: access, color: true}
			host := phosphor.New(size[0], size[1])
			host.Write([]byte(workspaceFrame(view, size[0], size[1])))
			label := "Access: Full (YOLO)"
			if access == workspaceCLISettings {
				label = "Access: CLI settings"
			}
			if !strings.Contains(strings.Join(host.Lines(), "\n"), label) || !strings.Contains(host.Line(size[1]-2), "f access") {
				t.Fatalf("%dx%d hid launch permissions", size[0], size[1])
			}
		}
	}
}

func TestWorkspaceCustomAgentCommandsKeepTheirArguments(t *testing.T) {
	bin := t.TempDir()
	record := filepath.Join(t.TempDir(), "arguments")
	shell := filepath.Join(bin, "shell-fixture")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$PTYD_TEST_ARGS\"\nprintf 'SHELL-%s-READY\\n' \"$2\"\nexec cat\n"
	if err := os.WriteFile(shell, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	for _, command := range []string{"codex", "claude"} {
		if err := os.WriteFile(filepath.Join(bin, command), []byte("#!/bin/sh\nprintf 'UNEXPECTED-BUILTIN\\n'\nexec cat\n"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("SHELL", shell)
	t.Setenv("PTYD_TEST_ARGS", record)
	d := startDaemon(t, freeAddr(t), "--persist-dir", t.TempDir())
	if _, err, code := d.cli(nil, "new", "-d", "custom"); code != 0 {
		t.Fatal(err)
	}
	w := d.open(120, 32, "attach", "custom")
	w.waitText("This group is ready.")
	for _, entry := range []string{"menu", "command"} {
		for _, command := range []string{"codex", "claude"} {
			if entry == "menu" {
				w.keys("n")
				w.waitText("Access: Full (YOLO)")
				w.keys("r")
				w.waitText("Run a command")
				w.keys(command + "\r")
			} else {
				w.keys(":run " + command + "\r")
			}
			w.waitText("SHELL-" + command + "-READY")
			raw, err := os.ReadFile(record)
			if err != nil {
				t.Fatal(err)
			}
			args := strings.Split(strings.TrimSpace(string(raw)), "\n")
			if !slices.Equal(args, []string{"-lc", command}) {
				t.Fatalf("%s custom %q was rewritten: %q", entry, command, args)
			}
			w.keys("jk")
			w.waitText("Returned to your workspace.")
		}
	}
	w.keys("q")
	if code := w.waitExit(); code != 0 {
		t.Fatal(code)
	}
}
