package terminal

import (
	"runtime"
	"strings"
	"testing"

	"github.com/2018kguo/ptyd/internal/benchdata"
)

// Bounds that keep per-event work independent of how much state the
// daemon holds. They are checked with allocation counts and bytes, not
// wall-clock time.

// allocs returns the mean allocations and bytes allocated per call of f.
func allocs(f func()) (n, bytes float64) {
	const runs = 50
	f() // warm up
	var a, b runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&a)
	for range runs {
		f()
	}
	runtime.ReadMemStats(&b)
	return float64(b.Mallocs-a.Mallocs) / runs, float64(b.TotalAlloc-a.TotalAlloc) / runs
}

// One line of output does the same work whether the daemon has one
// terminal or a thousand, and whether this terminal's line ring and
// screen scrollback are empty or full.
func TestOutputLineCostIndependentOfState(t *testing.T) {
	line := []byte("\x1b[32mINFO\x1b[0m request id=12345 path=/api/v1/items status=200\r\n")
	measure := func(others int, full bool) (float64, float64) {
		m := NewManager()
		for i := range others {
			fakeTerminal(m, i+1, 80, 24)
		}
		term := fakeTerminal(m, 0, 80, 24)
		if full {
			consumeChunked(term, benchdata.Logs(2<<20))
			if n := term.ring.n; n != ringCapacity {
				t.Fatalf("ring holds %d lines", n)
			}
			if n := term.screen.ScrollbackLen(); n < 1000 {
				t.Fatalf("scrollback holds %d lines", n)
			}
		}
		return allocs(func() { term.consume(line) })
	}
	n0, b0 := measure(0, false)
	n1, b1 := measure(1000, true)
	// Scrollback eviction and ring growth are amortized; the full case
	// only ever reuses.
	if n1 > n0 || b1 > b0+64 {
		t.Fatalf("one line: %.1f allocs / %.0f B with 1 empty terminal, %.1f / %.0f B with 1000 and full buffers", n0, b0, n1, b1)
	}
	if n1 > 3 {
		t.Fatalf("one line allocates %.1f times, want at most 3 (line text, scrollback line, ring growth)", n1)
	}
}

// Connecting a stream that only wants new lines costs the same against a
// full ring as against an empty one.
func TestSubscribeCostsWhatItReturns(t *testing.T) {
	measure := func(lines int) float64 {
		r := NewRing(ringCapacity, DefaultScrollbackBytes)
		r.Write([]byte(strings.Repeat("some output line\n", lines)))
		_, bytes := allocs(func() {
			id, _, snap := r.Subscribe(256, r.LatestSeq())
			if len(snap.Lines) != 0 {
				t.Fatalf("snapshot has %d lines", len(snap.Lines))
			}
			r.Unsubscribe(id)
		})
		return bytes
	}
	if empty, full := measure(0), measure(ringCapacity); full > empty+64 {
		t.Fatalf("subscribe allocates %.0f B against a full ring, %.0f B against an empty one", full, empty)
	}
}
