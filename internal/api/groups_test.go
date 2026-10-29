package api

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/2018kguo/ptyd/internal/terminal"
)

func TestGroupRoutesAndHumanInputOwnership(t *testing.T) {
	srv, m := startTestServer(t, terminal.Options{}, "test-token")
	request := func(method, path string, body, out any, want int) {
		t.Helper()
		var payload io.Reader
		if body != nil {
			b, e := json.Marshal(body)
			if e != nil {
				t.Fatal(e)
			}
			payload = bytes.NewReader(b)
		}
		req, e := http.NewRequest(method, srv.URL+path, payload)
		if e != nil {
			t.Fatal(e)
		}
		req.Header.Set("X-Ptyd-Token", "test-token")
		res, e := http.DefaultClient.Do(req)
		if e != nil {
			t.Fatal(e)
		}
		defer res.Body.Close()
		raw, e := io.ReadAll(res.Body)
		if e != nil {
			t.Fatal(e)
		}
		if res.StatusCode != want {
			t.Fatalf("%s %s: got %d want %d: %s", method, path, res.StatusCode, want, raw)
		}
		if out != nil {
			if e := json.Unmarshal(raw, out); e != nil {
				t.Fatal(e)
			}
		}
	}
	var a, b terminal.Group
	request("POST", "/groups", terminal.GroupConfig{Name: "work", CWD: t.TempDir()}, &a, 201)
	request("POST", "/groups", terminal.GroupConfig{Name: "review", CWD: t.TempDir()}, &b, 201)
	request("POST", "/groups", terminal.GroupConfig{Name: "WORK", CWD: t.TempDir()}, nil, 409)
	request("GET", "/overview?group=missing", nil, nil, 404)
	request("GET", "/terminals?group=missing", nil, nil, 404)
	request("POST", "/terminals", terminal.CreateConfig{GroupID: "missing", Name: "invalid", Command: "cat"}, nil, 404)
	var info terminal.Info
	request("POST", "/terminals", terminal.CreateConfig{GroupID: a.ID, Name: "worker", Command: "cat"}, &info, 201)
	if info.CWD != a.CWD {
		t.Fatalf("group directory was ignored: %+v", info)
	}
	request("POST", "/terminals/"+string(info.ID)+"/relaunch", map[string]string{"policy": "auto"}, nil, 409)
	archived := true
	request("PATCH", "/groups/"+string(a.ID), terminal.GroupUpdate{Archived: &archived}, nil, 409)
	request("DELETE", "/groups/"+string(a.ID), nil, nil, 409)
	request("DELETE", "/groups/"+string(a.ID)+"?force=typo", nil, nil, 400)
	human, e := m.Attach(info.ID, terminal.AttachOptions{Human: true, Rendered: true})
	if e != nil {
		t.Fatal(e)
	}
	request("POST", "/terminals/"+string(info.ID)+"/write", map[string]any{"data": "agent", "submit": true}, nil, 409)
	request("GET", "/overview?group="+string(a.ID), nil, nil, 200)
	human.Close()
	request("POST", "/terminals/"+string(info.ID)+"/write", map[string]any{"data": "agent continues", "submit": true}, nil, 200)
	name := "reviewer"
	request("PATCH", "/terminals/"+string(info.ID), terminal.TerminalUpdate{GroupID: &b.ID, Name: &name}, nil, 200)
	var old, new terminal.Overview
	request("GET", "/overview?group="+string(a.ID), nil, &old, 200)
	request("GET", "/overview?group="+string(b.ID), nil, &new, 200)
	if old.Counts.Total != 0 || new.Counts.Running != 1 || new.Sessions[0].ID != info.ID || new.Sessions[0].Name != name {
		t.Fatalf("move broke overview: old=%+v new=%+v", old, new)
	}
	request("DELETE", "/groups/"+string(a.ID), nil, nil, 200)
	request("DELETE", "/groups/"+string(b.ID)+"?force=true", nil, nil, 200)
	request("GET", "/terminals/"+string(info.ID), nil, nil, 404)
}
