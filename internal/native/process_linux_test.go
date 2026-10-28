package native

import "testing"

func TestLinuxProcessStatParsesCommandParentheses(t *testing.T) {
	p, err := parseLinuxProcessStat("23 (codex (old)) S 11 20 20 34818 20 0 0 0 0 0 0 0 0 0 0 0 1 0 12345 0 0")
	if err != nil || p.pid != 23 || p.ppid != 11 || p.group != 20 || p.tty != 34818 || p.foreground != 20 || p.generation != "12345" || p.name != "codex (old)" {
		t.Fatalf("%+v %v", p, err)
	}
	if _, err := parseLinuxProcessStat("23 codex S 1"); err == nil {
		t.Fatal("malformed stat accepted")
	}
}
