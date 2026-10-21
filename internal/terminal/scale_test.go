package terminal

import (
	"context"
	"fmt"
	"math/rand/v2"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"
)

// retained is the reference model for what a ring keeps: the longest run
// of newest lines that fits both caps (always at least the newest line).
func retained(all []Line, maxLines, maxBytes int) []Line {
	n, cost := 0, 0
	for i := len(all) - 1; i >= 0; i-- {
		c := len(all[i].Text) + lineOverhead
		if n > 0 && (n >= maxLines || cost+c > maxBytes) {
			break
		}
		n, cost = n+1, cost+c
	}
	return all[len(all)-n:]
}

// Snapshot's O(1) seq lookup and read's chunked copy must return exactly
// what a scan of the reference model would, across ring growth,
// wraparound, and eviction by line count and by bytes.
func TestRingMatchesReferenceModel(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	for _, caps := range []struct{ lines, bytes int }{
		{1, 1 << 20}, {7, 1 << 20}, {50, 1 << 20}, {5000, 20 << 10}, {64, 40 << 10},
	} {
		r := NewRing(caps.lines, caps.bytes)
		maxBytes := r.maxBytes
		var all []Line
		for step := range 400 {
			text := strings.Repeat("x", rng.IntN(3)*rng.IntN(4000))
			if step%3 == 0 {
				text = fmt.Sprint(step)
			}
			r.Write([]byte(text + "\n"))
			all = append(all, Line{Seq: int64(len(all)), Text: text})

			want := retained(all, caps.lines, maxBytes)
			r.mu.Lock()
			bytes, slots := r.bytes, len(r.lines)
			r.mu.Unlock()
			if bytes > maxBytes || slots > caps.lines {
				t.Fatalf("caps %v: %d bytes in %d slots", caps, bytes, slots)
			}
			latest := int64(len(all) - 1)
			for _, since := range []int64{-5, -1, 0, want[0].Seq - 1, want[0].Seq, latest - 3, latest - 1, latest, latest + 7} {
				for _, tail := range []int{0, 1, 3, 1 << 20} {
					exp := slices.DeleteFunc(slices.Clone(want), func(l Line) bool { return l.Seq <= since })
					if tail > 0 && len(exp) > tail {
						exp = exp[len(exp)-tail:]
					}
					snap := r.Snapshot(since, tail)
					if snap.LatestSeq != latest || !sameLines(snap.Lines, exp) {
						t.Fatalf("caps %v step %d Snapshot(%d, %d): got %d lines from %v, want %d from %v",
							caps, step, since, tail, len(snap.Lines), firstSeq(snap.Lines), len(exp), firstSeq(exp))
					}
					if tail == 0 {
						var got []Line
						buf := make([]Line, 3)
						for s, n := since, r.read(buf, since); n > 0; n = r.read(buf, s) {
							got = append(got, buf[:n]...)
							s = buf[n-1].Seq
						}
						if !sameLines(got, exp) {
							t.Fatalf("caps %v step %d read(%d): got %d lines, want %d", caps, step, since, len(got), len(exp))
						}
					}
				}
			}
		}
	}
}

func sameLines(got, want []Line) bool {
	return slices.EqualFunc(got, want, func(a, b Line) bool { return a.Seq == b.Seq && a.Text == b.Text })
}

func firstSeq(lines []Line) int64 {
	if len(lines) == 0 {
		return -1
	}
	return lines[0].Seq
}

// A terminal printing maximal lines keeps its memory near the byte budget,
// not lines × maxLineBytes.
func TestRingByteBudget(t *testing.T) {
	const budget = 256 << 10
	r := NewRing(5000, budget)
	long := []byte(strings.Repeat("y", maxLineBytes-1) + "\n")
	for range 1000 {
		r.Write(long)
	}
	snap := r.Snapshot(-1, 0)
	if snap.LatestSeq != 999 {
		t.Fatalf("latest seq %d, want 999", snap.LatestSeq)
	}
	if want := budget / (maxLineBytes - 1 + lineOverhead); len(snap.Lines) != want {
		t.Fatalf("retained %d lines, want %d", len(snap.Lines), want)
	}
	if last := snap.Lines[len(snap.Lines)-1]; last.Seq != 999 {
		t.Fatalf("newest line evicted: %+v", last.Seq)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.bytes > budget {
		t.Fatalf("retained %d bytes, budget %d", r.bytes, budget)
	}
}

// An idle ring holds no line storage; it grows with what it retains.
func TestRingAllocatesLazily(t *testing.T) {
	r := NewRing(5000, DefaultScrollbackBytes)
	if cap(r.lines) != 0 {
		t.Fatalf("empty ring preallocated %d slots", cap(r.lines))
	}
	r.Write([]byte("a\nb\n"))
	if n := len(r.lines); n == 0 || n > 16 {
		t.Fatalf("two lines use %d slots", n)
	}
}

// seqBefore must agree with a linear scan of line timestamps.
func TestRingSeqBefore(t *testing.T) {
	r := NewRing(10, DefaultScrollbackBytes)
	for i := range 25 { // wraps a 10-slot ring
		r.Write(fmt.Appendf(nil, "%d\n", i))
	}
	base := time.Now()
	r.mu.Lock()
	for i := range r.n {
		r.lines[(r.head+i)%len(r.lines)].At = base.Add(time.Duration(i) * time.Second)
	}
	r.mu.Unlock()
	lines := r.Snapshot(-1, 0).Lines
	for off := -2; off <= 12; off++ {
		cut := base.Add(time.Duration(off)*time.Second - time.Millisecond)
		want := lines[0].Seq - 1
		for _, l := range lines {
			if l.At.Before(cut) {
				want = l.Seq
			}
		}
		if got := r.seqBefore(cut); got != want {
			t.Errorf("seqBefore(base%+ds) = %d, want %d", off, got, want)
		}
	}
}

// Split points must not change the result: the span-based writer has to
// agree with a byte-at-a-time write of the same stream.
func TestRingWriteChunkingInvariant(t *testing.T) {
	in := append(slices.Clone(benchOutput[:200<<10]), "tail\r\nx\ry\r"...)
	in = append(in, strings.Repeat("é", maxLineBytes)...)
	whole := NewRing(ringCapacity, DefaultScrollbackBytes)
	whole.Write(in)
	bytewise := NewRing(ringCapacity, DefaultScrollbackBytes)
	for i := range in {
		bytewise.Write(in[i : i+1])
	}
	random := NewRing(ringCapacity, DefaultScrollbackBytes)
	rng := rand.New(rand.NewPCG(3, 4))
	for rest := in; len(rest) > 0; {
		n := min(len(rest), 1+rng.IntN(300))
		random.Write(rest[:n])
		rest = rest[n:]
	}
	want := whole.Snapshot(-1, 0)
	for name, r := range map[string]*Ring{"bytewise": bytewise, "random": random} {
		got := r.Snapshot(-1, 0)
		if !sameLines(got.Lines, want.Lines) || got.Pending != want.Pending {
			t.Errorf("%s chunking: %d lines pending %q; whole: %d lines pending %q",
				name, len(got.Lines), got.Pending, len(want.Lines), want.Pending)
		}
	}
}

// ansiPattern is the regular expression stripANSI replaced; it is kept here
// as the oracle the hand-written stripper must agree with.
var ansiPattern = regexp.MustCompile(
	`\x1b\[[\x30-\x3f]*[\x20-\x2f]*[\x40-\x7e]` +
		`|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)?` +
		`|\x1b[^\x5b\x5d]`,
)

func TestStripANSIEdgeCases(t *testing.T) {
	cases := map[string]string{
		"\x1b]8;;http://x\x1b\\link\x1b]8;;\x1b\\": "link",         // OSC 8 hyperlink, ST-terminated
		"\x1b]0;unterminated":                      "",             // OSC to end of text
		"\x1b]0;a\x1bb":                            "",             // OSC ended by ESC b
		"a\x1b":                                    "a\x1b",        // lone trailing ESC kept
		"a\x1b[":                                   "a\x1b[",       // CSI without final byte kept
		"a\x1b[31":                                 "a\x1b[31",     // partial CSI kept until complete
		"\x1b[31\x07m":                             "\x1b[31\x07m", // invalid CSI byte: not a sequence
		"\x1b[ q":                                  "",             // CSI with intermediate byte
		"\x1bé!":                                   "!",            // ESC + multi-byte rune
		"\x1b\x1b[0m":                              "[0m",          // ESC ESC is a two-byte sequence
		"\x1b(Bx":                                  "Bx",           // ESC ( (charset) drops one char
		"héllo\x1b[0m wörld":                       "héllo wörld",
	}
	for in, want := range cases {
		if got := stripANSI(in); got != want {
			t.Errorf("stripANSI(%q) = %q, want %q", in, got, want)
		}
		if oracle := ansiPattern.ReplaceAllString(in, ""); oracle != want {
			t.Errorf("oracle(%q) = %q, want %q", in, oracle, want)
		}
	}
}

func TestStripANSIMatchesRegexp(t *testing.T) {
	alphabet := []string{"\x1b", "[", "]", "\x07", "\\", "0", ";", "?", " ", "m", "K", "a", "é", "\xff", "\x80", "\n"}
	rng := rand.New(rand.NewPCG(5, 6))
	for range 50000 {
		var b strings.Builder
		for range rng.IntN(12) {
			b.WriteString(alphabet[rng.IntN(len(alphabet))])
		}
		in := b.String()
		if got, want := stripANSI(in), ansiPattern.ReplaceAllString(in, ""); got != want {
			t.Fatalf("stripANSI(%q) = %q, regexp gives %q", in, got, want)
		}
	}
}

func FuzzStripANSI(f *testing.F) {
	for _, s := range []string{"\x1b[31mred\x1b[0m", "\x1b]0;t\x07x", "\x1b]0;t\x1b\\x", "a\x1b", "\x1b[", "\x1bé"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, in string) {
		if got, want := stripANSI(in), ansiPattern.ReplaceAllString(in, ""); got != want {
			t.Fatalf("stripANSI(%q) = %q, regexp gives %q", in, got, want)
		}
	})
}

func BenchmarkStripANSI(b *testing.B) {
	plain := "-rw-r--r--  1 user  staff  4096 Oct 21 00:22 file.go"
	colored := "\x1b[1;32m   Compiling\x1b[0m crate v0.1.0 \x1b[31merror\x1b[0m\x1b[K"
	for name, s := range map[string]string{"plain": plain, "colored": colored} {
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				stripANSI(s)
			}
		})
		b.Run(name+"/regexp", func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				ansiPattern.ReplaceAllString(s, "")
			}
		})
	}
}

// processPending reports how long until pending text settles instead of
// being polled, fires once it has, and does not fire twice for one prompt.
func TestProcessPendingSettles(t *testing.T) {
	term := &terminal{ring: NewRing(10, DefaultScrollbackBytes)}
	term.watches = []*compiledWatch{{spec: WatchSpec{Name: "q"}, re: regexp.MustCompile(`\?\s*$`)}}
	if d := term.processPending(); d != 0 {
		t.Fatalf("no pending text: wait %v, want 0", d)
	}
	term.ring.Write([]byte("continue? "))
	term.lastOutputAt = time.Now()
	if d := term.processPending(); d <= 0 || d > pendingQuietTime {
		t.Fatalf("fresh pending: wait %v, want (0, %v]", d, pendingQuietTime)
	}
	if len(term.attention) != 0 {
		t.Fatal("watch fired before pending text settled")
	}
	term.lastOutputAt = time.Now().Add(-2 * pendingQuietTime)
	for range 3 {
		if d := term.processPending(); d != 0 {
			t.Fatalf("settled pending: wait %v, want 0", d)
		}
	}
	if len(term.attention) != 1 || term.attention[0].Line.Seq != -1 {
		t.Fatalf("want exactly one pending attention event, got %+v", term.attention)
	}
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for deadline := time.Now().Add(3 * time.Second); !cond(); time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
	}
}

// assertAsleep checks that term's watch/wait loops make no passes over
// several pending-quiet periods (the old loops ticked every period).
func assertAsleep(t *testing.T, term *terminal) {
	t.Helper()
	before := term.passes.Load()
	time.Sleep(3 * pendingQuietTime)
	if n := term.passes.Load() - before; n != 0 {
		t.Fatalf("idle terminal woke %d times", n)
	}
}

// Idle terminals must not wake up: watches and waits sleep until output
// arrives (or pending text is due to settle) instead of polling.
func TestIdleTerminalDoesNotWake(t *testing.T) {
	m := NewManager()
	info, err := m.Create(CreateConfig{
		Name: "idle", Command: "sh", Args: []string{"-c", "printf 'ready? '; exec cat"},
		Watches: []WatchSpec{{Name: "prompt", Pattern: `\?\s*$`}},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close(info.ID)
	term, _ := m.get(info.ID)

	// The prompt arrives, settles and fires the watch; then nothing.
	waitFor(t, "prompt watch", func() bool { got, _ := m.Get(info.ID); return len(got.Attention) == 1 })
	assertAsleep(t, term)

	// A Wait that cannot match makes one pass, then sleeps until its
	// deadline.
	start := term.passes.Load()
	ctx, cancel := context.WithTimeout(context.Background(), 3*pendingQuietTime)
	defer cancel()
	if _, _, err := m.Wait(ctx, info.ID, "never", -1, 0); err != context.DeadlineExceeded {
		t.Fatalf("wait: %v", err)
	}
	if n := term.passes.Load() - start; n != 1 {
		t.Fatalf("idle wait made %d passes, want 1", n)
	}

	// Output wakes the watch loop, which then sleeps again.
	if err := m.Write(info.ID, "hello\r"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "watch loop to wake", func() bool { return term.passes.Load() > start+1 })
	waitFor(t, "echo", func() bool { return latestSeq(m, info.ID) >= 1 })
	assertAsleep(t, term)
}

func latestSeq(m *Manager, id ID) int64 {
	info, _ := m.Get(id)
	return info.LatestSeq
}

// Terminals without watches run no watch loop at all.
func TestNoWatchLoopWithoutWatches(t *testing.T) {
	m := NewManager()
	info, err := m.Create(CreateConfig{Name: "plain", Command: "sh", Args: []string{"-c", "echo hi; exec cat"}})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close(info.ID)
	term, _ := m.get(info.ID)
	waitFor(t, "output", func() bool { return latestSeq(m, info.ID) >= 0 })
	if n := term.passes.Load(); n != 0 {
		t.Fatalf("watch loop made %d passes for a terminal without watches", n)
	}
}

// Resize must reach the PTY (and, on Linux, keep the master pollable).
func TestResizeReachesPTY(t *testing.T) {
	m := NewManager()
	info, err := m.Create(CreateConfig{Name: "size", Command: "sh", Args: []string{"-c", "read x; stty size; exec cat"}})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close(info.ID)
	if err := m.Resize(info.ID, 123, 45); err != nil {
		t.Fatalf("resize: %v", err)
	}
	if err := m.Write(info.ID, "\r"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, _, err := m.Wait(ctx, info.ID, `^45 123$`, -1, 0); err != nil {
		out, _ := m.Output(info.ID, -1, 0)
		t.Fatalf("wait for new size: %v; output %+v", err, out.Lines)
	}
}
