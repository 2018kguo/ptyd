package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/2018kguo/ptyd/internal/terminal"
)

func TestWorkspaceUsesConversationTitlesAndProviderLabels(t *testing.T) {
	t.Setenv("TERM", "xterm-256color")
	t.Setenv("PTYD_REDUCED_MOTION", "1")
	d := startDaemon(t, freeAddr(t))
	bin := t.TempDir()
	for _, name := range []string{"codex", "claude"} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\nprintf '\\033]2;%s\\aREADY\\n' \"$1\"\nwhile IFS= read -r line; do if [ \"$line\" = update ]; then printf '\\033]2;Updated integration title\\a'; fi; done\n"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	group, stderr, code := d.cli(nil, "new", "-d", "titles")
	if code != 0 {
		t.Fatal(stderr)
	}
	for _, row := range []struct{ name, provider, title string }{
		{"codex", "codex", "Investigate AOP save failure"},
		{"claude", "claude", "◑ Native Salesforce integration"},
		{"my custom name", "codex", "Keep my custom name"},
	} {
		var info terminal.Info
		if code := d.do("POST", "/terminals", terminal.CreateConfig{GroupID: terminal.GroupID(strings.TrimSpace(group)), Name: row.name, Command: filepath.Join(bin, row.provider), Args: []string{row.title}}, &info); code != 201 {
			t.Fatalf("create fake %s: %d", row.provider, code)
		}
	}
	w := d.open(200, 50, "attach", "titles")
	w.waitText("Investigate AOP save failure")
	w.waitText("Native Salesforce integra")
	w.waitText("Codex · ● Running")
	w.waitText("Claude · ● Running")
	w.waitText("my custom name")
	w.keys("jv")
	w.waitText("Claude · Native Salesforce integration")
	w.keys("\r")
	w.waitText("titles / Claude · Native Salesforce integration")
	w.keys("update\r")
	w.waitText("Claude · Updated integration title")
	w.keys("jk")
	w.waitText("TERMINALS")
	w.waitText("Updated integration title")
	w.keys("q")
	if code := w.waitExit(); code != 0 {
		t.Fatal(code)
	}
}
