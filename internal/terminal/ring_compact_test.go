package terminal

import (
	"bytes"
	"context"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"
)

func fillColdRing(r *Ring, shortLines int) {
	r.Write(bytes.Repeat([]byte("x\n"), shortLines))
	long := append(bytes.Repeat([]byte("x"), maxLineBytes-1), '\n')
	for range 64 {
		r.Write(long)
	}
}

func cloneColdRing(r *Ring) *Ring {
	return &Ring{
		lines: append([]Line(nil), r.lines...), head: r.head, n: r.n,
		bytes: r.bytes, maxLines: r.maxLines, maxBytes: r.maxBytes, nextSeq: r.nextSeq,
		pending: bytes.Clone(r.pending), pendingCR: r.pendingCR, closed: r.closed,
	}
}

func assertColdRingEqual(t *testing.T, a, b *Ring) {
	t.Helper()
	for _, since := range []int64{-1, a.nextSeq - int64(a.n), a.nextSeq - 4, a.nextSeq, 1 << 40} {
		for _, tail := range []int{0, 1, 7, 5000} {
			if !reflect.DeepEqual(a.Snapshot(since, tail), b.Snapshot(since, tail)) {
				t.Fatalf("snapshot changed: since=%d tail=%d", since, tail)
			}
		}
	}
	for _, at := range []time.Time{{}, time.Now(), time.Now().Add(time.Hour)} {
		if a.seqBefore(at) != b.seqBefore(at) {
			t.Fatal("timestamp lookup changed")
		}
	}
	ca, cb := Cursor{ring: a, last: -1}, Cursor{ring: b, last: -1}
	for {
		var aa, bb [7]Line
		na, nb := ca.Read(aa[:]), cb.Read(bb[:])
		if na != nb || !reflect.DeepEqual(aa[:na], bb[:nb]) {
			t.Fatal("cursor output changed")
		}
		if na == 0 {
			break
		}
	}
}

func TestRingCompactPreservesContinuation(t *testing.T) {
	for _, tc := range []struct {
		name   string
		prefix []byte
		tail   [][]byte
	}{
		{"prompt", []byte("prompt$ "), [][]byte{[]byte("command\n"), []byte("next\n")}},
		{"crlf", []byte("progress\r"), [][]byte{[]byte("\n"), []byte("next\n")}},
		{"overwrite", []byte("progress\r"), [][]byte{[]byte("done\r"), []byte("\n")}},
		{"ansi", []byte("\x1b[31mprompt\x1b["), [][]byte{[]byte("0"), []byte("mcontinued\n")}},
		{"unicode", []byte{'p', 0xf0, 0x9f}, [][]byte{{0x91}, {0xa9, '\n'}}},
		{"unicode-limit", append(bytes.Repeat([]byte{'a'}, maxLineBytes-2), 0xf0, 0x9f), [][]byte{{0x91, 0xa9}, []byte("\n")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newBenchRing()
			// Rotate until the retained long lines straddle the backing array.
			fillColdRing(r, 2*ringCapacity-40)
			if r.head+r.n <= len(r.lines) {
				t.Fatal("fixture did not wrap retained lines")
			}
			r.Write(tc.prefix)
			control := cloneColdRing(r)
			changed, ended := r.Changed()
			if ended {
				t.Fatal("ring ended")
			}
			oldSlots, oldPending := len(r.lines), cap(r.pending)
			r.Compact()
			assertColdRingEqual(t, r, control)
			if len(r.lines) >= oldSlots || (len(tc.prefix) < 512 && cap(r.pending) >= oldPending) {
				t.Fatal("high-water storage was not reclaimed")
			}
			if again, _ := r.Changed(); again != changed {
				t.Fatal("compaction replaced the notification channel")
			}
			select {
			case <-changed:
				t.Fatal("compaction woke a reader")
			default:
			}
			for i, p := range tc.tail {
				at := time.Now().Add(time.Duration(i+1) * time.Hour)
				r.now, control.now = at, at
				r.Write(p)
				control.Write(p)
				assertColdRingEqual(t, r, control)
				r.Compact()
			}
			r.FlushPending()
			control.FlushPending()
			assertColdRingEqual(t, r, control)
		})
	}
}

func TestRingCompactLeavesReusableStorage(t *testing.T) {
	r := newBenchRing()
	r.Compact()
	if r.lines != nil || r.pending != nil {
		t.Fatal("empty ring allocated storage")
	}
	fillColdRing(r, ringCapacity)
	r.Write([]byte("prompt$ "))
	r.Compact()
	if len(r.lines) <= r.n || cap(r.pending) < len(r.pending)+256 {
		t.Fatal("compaction discarded burst headroom")
	}
	if n := testing.AllocsPerRun(10, r.Compact); n != 0 {
		t.Fatalf("repeated compaction allocated %.0f times", n)
	}
	if n := testing.AllocsPerRun(10, func() { r.Write([]byte(".")) }); n != 0 {
		t.Fatalf("resumed prompt allocated %.0f times", n)
	}
	r.Close()
	before := r.Snapshot(-1, 0)
	ch, ended := r.Changed()
	r.Compact()
	if next, closed := r.Changed(); !ended || !closed || next != ch || !reflect.DeepEqual(before, r.Snapshot(-1, 0)) {
		t.Fatal("closed ring changed during compaction")
	}
}

func TestRingCompactPreservesWaitingReader(t *testing.T) {
	m := NewManager()
	term := fakeTerminal(m, 0, 80, 24)
	fillColdRing(term.ring, 2*ringCapacity-40)
	since := term.ring.LatestSeq()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		line, before, err := m.Wait(ctx, term.info.ID, "^MATCH$", since, 3)
		if err == nil && (line.Text != "MATCH" || len(before) != 3 || before[2].Seq != since) {
			err = fmt.Errorf("unexpected wait result: match=%+v context=%+v", line, before)
		}
		done <- err
	}()
	waitFor(t, "wait scanning retained output", func() bool { return term.passes.Load() > 0 })
	term.ring.Compact()
	term.ring.Write([]byte("MATCH\n"))
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestRingCloseReclaimsColdStorage(t *testing.T) {
	r := newBenchRing()
	fillColdRing(r, ringCapacity)
	r.Write([]byte("\x1b["))
	before := r.Snapshot(-1, 0)
	oldSlots, oldPending := len(r.lines), cap(r.pending)
	changed, _ := r.Changed()
	r.Close()
	if !reflect.DeepEqual(before, r.Snapshot(-1, 0)) || len(r.lines) >= oldSlots || cap(r.pending) >= oldPending {
		t.Fatal("close did not reclaim storage while preserving output")
	}
	select {
	case <-changed:
	default:
		t.Fatal("close did not notify its reader")
	}
	if _, ended := r.Changed(); !ended {
		t.Fatal("close did not mark the output ended")
	}
}

func TestRingCompactConcurrentOutput(t *testing.T) {
	r := newBenchRing()
	fillColdRing(r, ringCapacity)
	var wg sync.WaitGroup
	wg.Go(func() {
		for i := range 1000 {
			r.Write(fmt.Appendf(nil, "line-%d\n", i))
		}
	})
	wg.Go(func() {
		for range 1000 {
			r.Compact()
			r.Snapshot(-1, 3)
		}
	})
	wg.Wait()
	if last := r.Snapshot(-1, 1).Lines; len(last) != 1 || last[0].Text != "line-999" {
		t.Fatalf("last output: %+v", last)
	}
}
