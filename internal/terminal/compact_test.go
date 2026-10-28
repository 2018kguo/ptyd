package terminal

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestIdleCheckpointKeepsScreenAndContinuation(t *testing.T) {
	m := NewManagerWithOptions(Options{PersistDir: t.TempDir()})
	term := fakeTerminal(m, 0, 80, 24)
	dir := terminalDir(m.opts.PersistDir, term.info.ID)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	term.persist = &persister{dir: dir}
	defer term.stopPersisting()
	fillColdRing(term.ring, ringCapacity)
	// Populate the backing rows, erase them, and leave both a prompt and
	// an unfinished CSI at the boundary where output becomes quiet.
	for range 30 {
		term.consume([]byte("old output\r\n"))
	}
	term.consume([]byte("\x1b[2J\x1b[Hprompt$ \x1b[3"))
	term.persist.stopTimer()
	before := term.screen.Repaint(true)
	beforeRing := term.ring.Snapshot(-1, 0)
	beforeSlots, beforePending := len(term.ring.lines), cap(term.ring.pending)
	term.mu.Lock()
	term.lastOutputAt = time.Now().Add(-2 * ckptSettle)
	term.mu.Unlock()
	term.settleFired()
	if !reflect.DeepEqual(beforeRing, term.ring.Snapshot(-1, 0)) {
		t.Fatal("idle checkpoint changed retained output")
	}
	if len(term.ring.lines) >= beforeSlots || cap(term.ring.pending) >= beforePending {
		t.Fatal("idle checkpoint did not reclaim ring storage")
	}
	if _, err := os.Stat(filepath.Join(dir, ckptFile)); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, term.screen.Repaint(true)) {
		t.Fatal("idle checkpoint changed the screen")
	}
	control, _, err := loadScreen(dir, 80, 24)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, control.Repaint(true)) {
		t.Fatal("stored idle checkpoint changed the screen")
	}
	suffix := []byte("1mcontinued\x1b[0m")
	term.consume(suffix)
	control.Write(suffix)
	if !bytes.Equal(control.Repaint(true), term.screen.Repaint(true)) {
		t.Fatal("idle compaction changed continued output")
	}
}
