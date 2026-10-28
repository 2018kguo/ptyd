package terminal

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

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
		{"underline-colors", "\x1b]8;id=main;https://main\x1b\\\x1b[4:3;58:2::1:2:3mMAIN\x1b7\x1b[?1049h\x1b[58;5;9mALT\x1b7", "\x1b[59mX\x1b8Y\x1b[?1049l\x1b8Z", 12, 5},
		{"independent-mouse", "\x1b[?9;1000;1002;1003;1005;1006;1015;1016h\x1b[?9;1005s\x1b[?9;1005l", "\x1b[?9;1005r\x1b[?1000;1006l", 12, 5},
		{"long-cluster", "\x1b[?2027hA" + strings.Repeat("\U000e0100", 64), "\u0301X", 12, 5},
		{"history-blanks", "A\x1b[5GZ\r\nB \x1b[5GY\r\nC\r\nD", "\r\nE", 8, 3},
		{"semantic-prompts", "output\r\n\x1b]133;A\a$ \x1b]133;B\a", "typed", 12, 5},
		{"partial-semantic", "\x1b]133;A;redraw=", "0\a$ ", 12, 5},
		{"saved-single-shift", "\x1b*A\x1bN\x1b7#", "\x1b8#", 12, 5},
		{"saved-live-hyperlink", "\x1b]8;;https://old\aA\x1b7\x1b]8;;\a", "\x1b8B", 12, 5},
		{"closed-alternate", "MAIN\x1b[?1049h\x1b[4:3;58;5;9mALT\x1b[?1049l", "\x1b[?47hX", 12, 5},
		{"independent-screen-modes", "\x1b[?47h\x1b[?1049h\x1b[?1049l", "\x1b[?47l\x1b[?1049hX\x1b[?1049l", 12, 5},
		{"column-modes", "\x1b[?40h\x1b[?3h\x1b[?3;40s\x1b[?40lwide", "\x1b[?40r\x1b[?3l", 12, 5},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			original := phosphor.New(tc.cols, tc.rows)
			original.Write([]byte(tc.before))
			switch tc.name {
			case "underline-colors":
				if c := original.Cell(4, 0); c.Style.UnderlineColor != phosphor.IndexedColor(9) || c.Link.URI != "" {
					t.Fatalf("underline/link input was not represented: %+v", c)
				}
			case "independent-mouse":
				m := original.Modes()
				if m.MouseTrackingModes != phosphor.MouseTrackingNormal|phosphor.MouseTrackingButtonEvent|phosphor.MouseTrackingAnyEvent || m.MouseEncodingModes != phosphor.MouseEncodingModeSGR|phosphor.MouseEncodingModeURXVT|phosphor.MouseEncodingModeSGRPixels {
					t.Fatalf("independent mouse bits missing: %+v", m)
				}
			case "long-cluster":
				if c := original.Cell(0, 0); utf8.RuneCountInString(c.Combining) != 64 {
					t.Fatalf("native-sized cluster was truncated: %+v", c)
				}
			}
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

func TestReleasedBinaryCheckpointRemainsReadable(t *testing.T) {
	// This is the actual v0.3.0 library writer's format, used by ptyd
	// v0.2.0 under checkpoint header version 2. Do not regenerate it.
	body, err := os.ReadFile("testdata/phosphor-v030.snapshot")
	if err != nil {
		t.Fatal(err)
	}
	original := new(phosphor.Terminal)
	if err = original.RestoreSnapshot(body, phosphor.SnapshotLimits{}); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err = writeCheckpoint(dir, 12, 5, 9, body); err != nil {
		t.Fatal(err)
	}
	suffix := []byte("\x1b8X\x1b[?1049lY\x1b8Z\x1b[58;2;12;34;56mU")
	p := &persister{dir: dir, gen: 9}
	p.appendLocked(recData, suffix)
	p.tail.close()
	original.Write(suffix)
	restored, gen, err := loadScreen(dir, 1, 1)
	if err != nil || gen != 10 {
		t.Fatalf("released binary restore: gen%d err%v", gen, err)
	}
	if !bytes.Equal(restored.Snapshot(), original.Snapshot()) {
		t.Fatal("released binary checkpoint plus tail changed")
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
