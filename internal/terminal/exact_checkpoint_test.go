package terminal

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/2018kguo/phosphor"
)

func TestExactCheckpointContinuesThroughTail(t *testing.T) {
	for _, tc := range []struct {
		name, before, after string
		cols, rows          int
	}{
		{"rep", "AB\x1b[2J\x1b[H", "\x1b[3b", 8, 4},
		{"inactive-screen", "MAIN\x1b7\x1b[?1049h\x1b[31mALT\x1b7", "\x1b[?1049l!\x1b8X", 8, 4},
		{"outside-origin", "A\x1b[2;3r\x1b[?69h\x1b[3;6s\x1b[?6hB\x1b[22J", "X", 8, 4},
		{"grapheme", "\x1b[?2027hA❤️\x1b[?2027l", "\x1b[?2027h\ufe0eX", 3, 4},
		{"partial-utf8", "X\xf0\x9f", "\x98\x80Y", 8, 4},
		{"partial-csi", "X\x1b[38:2::1:2:", "3mY", 8, 4},
		{"partial-osc", "X\x1b]8;id=x;https://example", ".com\x1b\\Y", 8, 4},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			original := phosphor.New(tc.cols, tc.rows)
			original.Write([]byte(tc.before))
			term := &terminal{screen: original, persist: &persister{dir: dir}}
			defer term.stopPersisting()
			term.checkpoint()
			term.persist.appendLocked(recData, []byte(tc.after))
			original.Write([]byte(tc.after))
			term.persist.appendLocked(recResize, []byte{0, 9, 0, 5})
			original.Resize(9, 5)
			term.persist.appendLocked(recData, []byte("\r\ncontinued"))
			original.Write([]byte("\r\ncontinued"))
			term.persist.tail.close()
			restored, next, err := loadScreen(dir, 1, 1)
			if err != nil {
				t.Fatal(err)
			}
			if next != 2 || !bytes.Equal(restored.Snapshot(), original.Snapshot()) {
				t.Fatalf("checkpoint plus tail lost emulator state (next=%d)", next)
			}
		})
	}
}

func TestLegacyCheckpointRemainsReadable(t *testing.T) {
	dir := t.TempDir()
	original := phosphor.New(8, 4)
	original.Write([]byte("old\r\ncheckpoint\x1b[3"))
	header, err := json.Marshal(ckptHeader{Version: 1, Cols: 8, Rows: 4, TailGen: 7})
	if err != nil {
		t.Fatal(err)
	}
	raw := append(append(header, '\n'), original.Repaint(true)...)
	if err := os.WriteFile(filepath.Join(dir, ckptFile), raw, 0600); err != nil {
		t.Fatal(err)
	}
	restored, gen, err := loadScreen(dir, 1, 1)
	if err != nil || gen != 7 {
		t.Fatalf("legacy restore: gen %d, err %v", gen, err)
	}
	original.Write([]byte("1mcontinued"))
	restored.Write([]byte("1mcontinued"))
	if !bytes.Equal(original.Repaint(true), restored.Repaint(true)) {
		t.Fatal("legacy checkpoint lost continuation")
	}
	if got, err := os.ReadFile(filepath.Join(dir, ckptFile)); err != nil || !bytes.Equal(got, raw) {
		t.Fatal("reading legacy checkpoint rewrote it")
	}
}

func TestExactCheckpointRejectsCorruption(t *testing.T) {
	for _, mismatch := range []bool{false, true} {
		dir := t.TempDir()
		scr := phosphor.New(8, 4)
		body := scr.Snapshot()
		cols := 8
		if mismatch {
			cols++
		} else {
			body[len(body)-1] ^= 1
		}
		if err := writeCheckpoint(dir, cols, 4, 1, body); err != nil {
			t.Fatal(err)
		}
		if _, _, err := loadScreen(dir, 8, 4); err == nil {
			t.Fatalf("accepted corrupt checkpoint (dimension mismatch %v)", mismatch)
		}
	}
}

func TestExactCheckpointKeepsAcceptedGeometry(t *testing.T) {
	dir := t.TempDir()
	// Sparse screens at this supported uint16 geometry exceed the
	// library's default virtual-cell limit without using large cell rows.
	scr := phosphor.New(3000, 2000)
	if err := writeCheckpoint(dir, 3000, 2000, 1, scr.Snapshot()); err != nil {
		t.Fatal(err)
	}
	got, _, err := loadScreen(dir, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if c, r := got.Size(); c != 3000 || r != 2000 {
		t.Fatalf("restored dimensions %dx%d", c, r)
	}
}

func TestCheckpointRejectsChangedVersion(t *testing.T) {
	dir := t.TempDir()
	scr := phosphor.New(8, 4)
	header, err := json.Marshal(ckptHeader{Version: 1, Cols: 8, Rows: 4, TailGen: 1})
	if err != nil {
		t.Fatal(err)
	}
	// A version 2 body under a version 1 header must not silently become
	// ANSI input, even though old checkpoints remain readable.
	raw := append(append(header, '\n'), scr.Snapshot()...)
	if err := os.WriteFile(filepath.Join(dir, ckptFile), raw, 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := loadScreen(dir, 8, 4); err == nil {
		t.Fatal("accepted snapshot body as legacy ANSI")
	}
}
