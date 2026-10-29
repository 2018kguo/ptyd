package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/2018kguo/ptyd/internal/client"
	"github.com/2018kguo/ptyd/internal/terminal"
)

func TestWorkspaceHomePreviewsAndContentSearch(t *testing.T) {
	t.Setenv("TERM", "xterm-256color")
	t.Setenv("PTYD_REDUCED_MOTION", "1")
	d := startDaemon(t, freeAddr(t))
	for _, g := range []string{"billing", "research"} {
		if _, err, code := d.cli(nil, "new", "-d", "--cwd", "/tmp", g); code != 0 {
			t.Fatal(err)
		}
	}
	ids := []string{}
	for _, row := range []struct{ group, name, script string }{
		{"billing", "codex", `printf 'ARCHIVE_INVOICE\r\n'; i=0; while [ $i -lt 30 ]; do printf 'older line %s\r\n' "$i"; i=$((i+1)); done; printf 'BILLING_READY'; exec cat`},
		{"billing", "claude", `printf 'SECOND_PANE_READY'; exec cat`},
		{"research", "codex", `printf 'RESEARCH_UNIQUE'; exec cat`},
	} {
		id, err, code := d.cli(nil, "term", "new", "-d", "--group", row.group, "-n", row.name, "--", "sh", "-c", row.script)
		if code != 0 {
			t.Fatal(err)
		}
		ids = append(ids, strings.TrimSpace(id))
	}
	w := d.open(160, 50, "attach", "billing")
	w.waitText("BILLING_READY")
	w.keys("jk")
	w.waitText("Your groups")
	w.waitText("BILLING_READY")
	w.waitText("SECOND_PANE_READY")
	w.waitText("jk back")
	w.keys("/RESEARCH_UNIQUE\r")
	w.waitText("match in screen")
	w.waitNoText("BILLING_READY")
	w.waitText("RESEARCH_UNIQUE")
	w.keys(":ls\r")
	w.waitNoText("match in screen")
	w.keys("/ARCHIVE_INVOICE\r")
	w.waitText("scrollback: ARCHIVE_INVOICE")
	w.waitNoText("RESEARCH_UNIQUE")
	w.keys("\r")
	w.waitText("TERMINALS")
	w.waitText("scrollback: ARCHIVE_INVOICE")
	w.keys("q")
	if code := w.waitExit(); code != 0 {
		t.Fatal(code)
	}
	for _, id := range ids {
		var info terminal.Info
		d.do("GET", "/terminals/"+id, nil, &info)
		var screen terminal.Screen
		d.do("GET", "/terminals/"+id+"/screen", nil, &screen)
		if info.Attached != 0 || info.HumanControl || screen.Cols != 80 || screen.Rows != 24 || info.Status != terminal.StatusRunning {
			t.Fatal("preview/search changed a live terminal", info.ID)
		}
	}
	out, err, code := d.cli(nil, "search", "--json", "--group", "billing", "archive_invoice")
	if code != 0 {
		t.Fatal(err, out)
	}
	var result client.ContentSearch
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatal(err, out)
	}
	if !result.Complete || len(result.Matches) != 1 || result.Matches[0].Source != "scrollback" || string(result.Matches[0].TerminalID) != ids[0] {
		t.Fatalf("CLI and UI search disagree: %+v", result)
	}
}
