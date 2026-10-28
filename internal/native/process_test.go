package native

import (
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"strings"
	"syscall"
	"testing"
)

func TestProcessObservationsNeverBecomeConversations(t *testing.T) {
	rows := []LocalProcess{
		{Provider: Claude, PID: 10, Generation: "1", Terminal: "/dev/ttys000", Group: 10},
		{Provider: Claude, PID: 11, Generation: "child", Terminal: "/dev/ttys000", Group: 10, Ancestors: []int{10}},
		{Provider: Codex, PID: 20, Generation: "2", Terminal: "/dev/ttys001", Group: 20},
		{Provider: Codex, PID: 21, Generation: "3", Terminal: "/dev/ttys001", Group: 20, Ancestors: []int{20}},
	}
	got := unlistedProcesses(rows, []Session{{WorkerPID: 10}})
	if len(got) != 1 || got[0].PID != 21 {
		t.Fatalf("native PID or same-tab duplicates leaked: %+v", got)
	}
	if rows[1].Key() == rows[2].Key() {
		t.Fatal("PID generation not part of observation identity")
	}
	if err := (Ref{Provider: got[0].Provider, ID: got[0].Key()}).Validate(); err == nil {
		t.Fatal("process key authorized native import")
	}
	if !reflect.DeepEqual(processAncestors(21, map[int]int{21: 20, 20: 19, 19: 21}), []int{20, 19}) {
		t.Fatal("ancestry loop did not stop")
	}
	raw, _ := json.Marshal(got)
	if strings.Contains(string(raw), "args") || strings.Contains(string(raw), "prompt") {
		t.Fatal("argv entered observation output")
	}
}
func TestProcessClassifierRejectsHelpers(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want bool
	}{
		{"codex", []string{"codex", "--yolo"}, true},
		{"codex", []string{"codex", "resume", "session"}, true},
		{"codex", []string{"codex", "app-server", "--listen", "unix://"}, false},
		{"codex", []string{"codex", "-c", "model=fixture", "app-server"}, false},
		{"claude", []string{"claude", "--", "--print"}, true},
		{"claude", []string{"claude", "--model", "sonnet", "daemon", "run"}, false},
		{"claude", []string{"claude", "--dangerously-skip-permissions"}, true},
		{"2.1.312", []string{"claude", "--dangerously-skip-permissions"}, true},
		{"claude", []string{"claude", "bg-spare", "socket"}, false},
		{"claude", []string{"claude", "daemon", "run"}, false},
		{"claude", []string{"claude", "agents", "--json"}, false},
		{"claude", []string{"claude", "-p", "private prompt"}, false},
		{"claude", []string{"claude", "-c", "-p", "private prompt"}, false},
		{"claude", []string{"claude", "--output-format", "text", "--print", "private prompt"}, false},
		{"claude", []string{"claude", "--continue", "--fork-session", "--print", "private prompt"}, false},
		{"codex", []string{"codex", "--profile", "work", "app-server"}, false},
		{"other", []string{"other"}, false},
	} {
		_, got := interactiveProcess(tc.name, tc.args)
		if got != tc.want {
			t.Errorf("%s %q = %v", tc.name, tc.args, got)
		}
	}
}

func TestProcessCoverageOnlyIgnoresExitRaces(t *testing.T) {
	for _, err := range []error{nil, os.ErrNotExist, syscall.ESRCH} {
		if processInspectionFailure(err) {
			t.Fatalf("exit race marked failure: %v", err)
		}
	}
	for _, err := range []error{os.ErrPermission, errors.New("malformed metadata"), errors.New("response exceeds limit")} {
		if !processInspectionFailure(err) {
			t.Fatalf("inspection gap hidden: %v", err)
		}
	}
	if processInspectionError(0) != nil || processInspectionError(2) == nil {
		t.Fatal("partial result not marked")
	}
}
