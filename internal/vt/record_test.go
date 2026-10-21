package vt

import (
	"io"
	"os"
	"os/exec"
	"sync"
	"testing"
	"time"

	"github.com/creack/pty"
)

// TestRecordPrograms records the real-program sessions in
// testdata/rec-*.raw: with VT_RECORD set to a directory holding a copy of
// testdata/rec-sample.txt named sample.txt, it runs nvim, vim, less, nano
// and man there on a 100x30 pseudo-terminal, types a scripted session and
// writes <name>.raw. Nothing is recorded otherwise.
func TestRecordPrograms(t *testing.T) {
	dir := os.Getenv("VT_RECORD")
	if dir == "" {
		t.Skip("set VT_RECORD to record")
	}
	const sample = "sample.txt"
	type step struct {
		keys  string
		pause time.Duration
	}
	ms := time.Millisecond
	progs := []struct {
		name  string
		argv  []string
		steps []step
	}{
		{"nvim", []string{"nvim", "-u", "NONE", "-i", "NONE", "-n", sample}, []step{
			{"", 800 * ms}, {":vsplit\r", 300 * ms}, {"\x04\x04", 300 * ms}, {"\x05\x05\x05", 300 * ms},
			{"\x17l", 200 * ms}, {"G", 300 * ms}, {"\x19\x19\x19\x19", 300 * ms}, {":split\r", 300 * ms},
			{"ggOinserted line with tabs\there and wide 漢字\x1b", 300 * ms}, {"\x06", 300 * ms}, {"\x02", 300 * ms},
			{"/reflow\r", 300 * ms}, {"n", 200 * ms}, {"dd", 200 * ms}, {"u", 200 * ms}, {":qa!\r", 500 * ms}}},
		{"vim", []string{"vim", "-u", "NONE", "-N", "-i", "NONE", "-n", sample}, []step{
			{"", 800 * ms}, {":vsplit\r", 300 * ms}, {"\x04\x04", 300 * ms}, {":set number\r", 200 * ms},
			{"G", 300 * ms}, {"\x15\x15", 300 * ms}, {"ggOnew line 漢字 café\x1b", 300 * ms},
			{":split\r", 300 * ms}, {"\x06\x06", 300 * ms}, {"/wrap\r", 200 * ms}, {":qa!\r", 500 * ms}}},
		{"less", []string{"less", sample}, []step{
			{"", 500 * ms}, {" ", 200 * ms}, {" ", 200 * ms}, {"b", 200 * ms}, {"/margin\r", 300 * ms},
			{"n", 200 * ms}, {"G", 300 * ms}, {"g", 300 * ms}, {"jjjjjkk", 200 * ms}, {"q", 400 * ms}}},
		{"nano", []string{"nano", "--ignorercfiles", sample}, []step{
			{"", 800 * ms}, {"hello from nano 漢字", 300 * ms}, {"\x16\x16", 300 * ms}, {"\x17cursor\r", 300 * ms},
			{"\x18", 300 * ms}, {"n", 600 * ms}}},
		{"man", []string{"man", "ls"}, []step{
			{"", 1200 * ms}, {" ", 300 * ms}, {" ", 300 * ms}, {"/-l\r", 300 * ms}, {"G", 300 * ms}, {"q", 500 * ms}}},
	}
	for _, p := range progs {
		cmd := exec.Command(p.argv[0], p.argv[1:]...)
		cmd.Env = []string{"TERM=xterm-256color", "LANG=en_US.UTF-8", "LC_ALL=en_US.UTF-8", "HOME=" + dir,
			"PATH=" + os.Getenv("PATH"), "MANPAGER=less", "PAGER=less"}
		cmd.Dir = dir
		f, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: 100, Rows: 30})
		if err != nil {
			t.Fatal(err)
		}
		var mu sync.Mutex
		var out []byte
		done := make(chan struct{})
		go func() {
			buf := make([]byte, 8192)
			for {
				n, err := f.Read(buf)
				mu.Lock()
				out = append(out, buf[:n]...)
				mu.Unlock()
				if err != nil {
					close(done)
					return
				}
			}
		}()
		for _, s := range p.steps {
			time.Sleep(s.pause)
			io.WriteString(f, s.keys)
		}
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			cmd.Process.Kill()
			<-done
		}
		cmd.Wait()
		mu.Lock()
		if err := os.WriteFile(dir+"/"+p.name+".raw", out, 0o644); err != nil {
			t.Fatal(err)
		}
		mu.Unlock()
	}
}
