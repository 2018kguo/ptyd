package client

import (
	"bytes"
	"strings"
	"testing"
)

func TestSessionCaptionCombinesProviderTitleAndCustomName(t *testing.T) {
	for _, test := range []struct{ name, provider, title, want string }{
		{"codex 2", "codex", "Investigate AOP save failure", "Codex · Investigate AOP save failure"},
		{"claude", "claude", "◑ Native Salesforce integration", "Claude · Native Salesforce integration"},
		{"claude", "claude", "⠂ Native Salesforce integration", "Claude · Native Salesforce integration"},
		{"release captain", "codex", "new conversation", "Codex · release captain"},
		{"codex", "codex", "", "Codex"},
		{"claude 3", "claude", "Claude Code", "Claude · claude 3"},
		{"server", "", "unrelated shell title", "server"},
	} {
		caption := SessionCaption{Name: test.name, Provider: test.provider}
		if got := caption.Session(test.title); got != test.want {
			t.Fatalf("%+v: %q", test, got)
		}
	}
	var out bytes.Buffer
	p := newPane(&out, "work / codex", 100, 20)
	p.caption = &SessionCaption{Name: "codex", Provider: "codex", Group: "work", Captain: true}
	p.Write([]byte("\x1b]2;Investigate AOP save failure\a"))
	if err := p.draw(true); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(p.statusKey.label, "work / ★ Codex · Investigate AOP save failure · Captain") {
		t.Fatal(p.statusKey.label)
	}
	p.Write([]byte("\x1b]2;Follow-up work\a"))
	if err := p.draw(false); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(p.statusKey.label, "Codex · Follow-up work") {
		t.Fatal("attached title stayed stale", p.statusKey.label)
	}
}
