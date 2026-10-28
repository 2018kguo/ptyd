package terminal

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestWaitContextAcrossOutputBursts(t *testing.T) {
	for _, before := range []int{-1, 0, 1, 3, 257, 10000, int(^uint(0) >> 1)} {
		for _, pending := range []bool{false, true} {
			t.Run(fmt.Sprintf("context=%d/pending=%v", before, pending), func(t *testing.T) {
				m := NewManager()
				term := fakeTerminal(m, 0, 80, 24)
				for i := range 300 {
					term.consume(fmt.Appendf(nil, "line-%d\n", i))
				}
				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer cancel()
				type result struct {
					line   Line
					before []Line
					err    error
				}
				done := make(chan result, 1)
				go func() {
					line, context, err := m.Wait(ctx, term.info.ID, "^MATCH$", 299, before)
					done <- result{line, context, err}
				}()
				waitFor(t, "wait reading the first burst", func() bool { return term.passes.Load() > 0 })
				for i := 300; i < 600; i++ {
					term.consume(fmt.Appendf(nil, "line-%d\n", i))
				}
				marker := "MATCH\n"
				if pending {
					marker = "MATCH"
				}
				term.consume([]byte(marker))
				got := <-done
				if got.err != nil || got.line.Text != "MATCH" {
					t.Fatalf("wait = %+v", got)
				}
				wantSeq := int64(600)
				if pending {
					wantSeq = -1
				}
				if got.line.Seq != wantSeq {
					t.Fatalf("match seq %d, want %d", got.line.Seq, wantSeq)
				}
				// Before since=299, only the requested context is fetched.
				wantCount := min(max(before, 0), 600)
				if len(got.before) != wantCount {
					t.Fatalf("context contains %d lines, want %d", len(got.before), wantCount)
				}
				for i, line := range got.before {
					seq := int64(600 - wantCount + i)
					if line.Seq != seq || line.Text != fmt.Sprintf("line-%d", seq) {
						t.Fatalf("context[%d] = %+v, want line-%d", i, line, seq)
					}
				}
			})
		}
	}
}

func TestWaitContextAllocationsDoNotGrowWithRetainedOutput(t *testing.T) {
	for _, before := range []int{0, 3} {
		t.Run(fmt.Sprintf("context=%d", before), func(t *testing.T) {
			measure := func(lines int) float64 {
				m := NewManager()
				term := fakeTerminal(m, 0, 80, 24)
				for range lines - 1 {
					// A short nonmatch avoids regexp's pooled matcher, whose
					// random eviction under -race would measure the matcher
					// instead of retained context.
					term.ring.Write([]byte("x\n"))
				}
				term.ring.Write([]byte("MATCH\n"))
				return testing.AllocsPerRun(3, func() {
					if _, _, err := m.Wait(context.Background(), term.info.ID, "^MATCH$", -1, before); err != nil {
						t.Fatal(err)
					}
				})
			}
			short, full := measure(1), measure(ringCapacity)
			if full > short+10 {
				t.Fatalf("retained output increased context allocations: one line %.0f, full window %.0f", short, full)
			}
		})
	}
}
