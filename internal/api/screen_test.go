package api

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/2018kguo/phosphor"
	"github.com/2018kguo/ptyd/internal/terminal"
)

func TestScreenEndpoint(t *testing.T) {
	srv, mgr := startTestServer(t, terminal.Options{}, "")
	info, err := mgr.Create(terminal.CreateConfig{
		Name: "s", Command: "sh", Cols: 30, Rows: 4,
		Args: []string{"-c", `printf 'one\r\ntwo\r\nthree\r\nfour\r\n\033[1;32mfive'; exec cat`},
	})
	if err != nil {
		t.Fatal(err)
	}
	var scr terminal.Screen
	for deadline := time.Now().Add(3 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		_, body := mustGet(t, srv.URL+"/terminals/"+string(info.ID)+"/screen?scrollback=1", nil)
		if err := json.Unmarshal(body, &scr); err != nil {
			t.Fatalf("decode %s: %v", body, err)
		}
		if len(scr.Lines) == 4 && scr.Lines[3] == "five" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("screen never showed output: %+v", scr)
		}
	}
	if strings.Join(scr.Lines, "|") != "two|three|four|five" || strings.Join(scr.Scrollback, "|") != "one" || scr.CursorX != 4 {
		t.Fatalf("screen %+v", scr)
	}
	resp, ansi := mustGet(t, srv.URL+"/terminals/"+string(info.ID)+"/screen?format=ansi", nil)
	if resp.StatusCode != 200 || !strings.Contains(string(ansi), "\x1b[0;1;32mfive") {
		t.Fatalf("ansi %d %q", resp.StatusCode, ansi)
	}
	fresh := phosphor.New(30, 4)
	fresh.Write(ansi)
	if strings.Join(fresh.Lines(), "|") != "two|three|four|five" {
		t.Fatalf("ansi repaint gives %q", fresh.Lines())
	}
	if resp, _ := mustGet(t, srv.URL+"/terminals/"+string(info.ID)+"/screen?format=bogus", nil); resp.StatusCode != 400 {
		t.Fatalf("bad format status %d", resp.StatusCode)
	}
}
