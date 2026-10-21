package terminal

import (
	"fmt"
	"os"
	"runtime"
	"runtime/pprof"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// benchOutput is ~1 MiB of PTY-shaped output (CRLF line endings): short
// shell lines, long log lines, colored ls/compiler output and \r progress
// bars, interleaved.
var benchOutput = func() []byte {
	var b strings.Builder
	for i := 0; b.Len() < 1<<20; i++ {
		switch i % 4 {
		case 0:
			fmt.Fprintf(&b, "$ ls -la\r\ntotal %d\r\n-rw-r--r--  1 user  staff  %d Oct 21 00:22 file%d.go\r\n", i, i*7, i)
		case 1:
			fmt.Fprintf(&b, "%s INFO request id=%d path=/api/v1/items %s\r\n", time.Unix(int64(i), 0).UTC().Format(time.RFC3339), i, strings.Repeat("k=v ", 100))
		case 2:
			fmt.Fprintf(&b, "\x1b[1;32m   Compiling\x1b[0m crate%d v0.1.%d\r\n\x1b[31merror\x1b[0m[E0%d]: \x1b[1mmismatched types\x1b[0m\x1b[K\r\n\x1b]0;title %d\x07", i, i, i%1000, i)
		case 3:
			for p := 0; p <= 100; p += 5 {
				fmt.Fprintf(&b, "\r[%-20s] %3d%%", strings.Repeat("=", p/5), p)
			}
			b.WriteString("\r\n")
		}
	}
	return []byte(b.String())
}()

// writeChunked feeds out to r in PTY-read-sized chunks.
func writeChunked(r *Ring, out []byte) {
	for len(out) > 0 {
		n := min(len(out), readBufSize)
		r.Write(out[:n])
		out = out[n:]
	}
}

func newBenchRing() *Ring { return NewRing(ringCapacity, DefaultScrollbackBytes) }

func BenchmarkRingWrite(b *testing.B) {
	r := newBenchRing()
	b.SetBytes(int64(len(benchOutput)))
	b.ReportAllocs()
	for b.Loop() {
		writeChunked(r, benchOutput)
	}
}

func fullBenchRing() *Ring {
	r := newBenchRing()
	for range 3 {
		writeChunked(r, benchOutput)
	}
	return r
}

func BenchmarkRingSnapshotSinceRecent(b *testing.B) {
	r := fullBenchRing()
	since := r.LatestSeq() - 10
	b.ReportAllocs()
	for b.Loop() {
		if n := len(r.Snapshot(since, 0).Lines); n != 10 {
			b.Fatalf("got %d lines", n)
		}
	}
}

func BenchmarkRingSnapshotTail(b *testing.B) {
	r := fullBenchRing()
	b.ReportAllocs()
	for b.Loop() {
		if n := len(r.Snapshot(-1, 100).Lines); n != 100 {
			b.Fatalf("got %d lines", n)
		}
	}
}

// BenchmarkRingMemory reports the heap retained by one ring that is empty,
// holds typical output, or holds the worst case (every line maxLineBytes).
func BenchmarkRingMemory(b *testing.B) {
	long := []byte(strings.Repeat("x", maxLineBytes-1) + "\n")
	cases := []struct {
		name string
		fill func(*Ring)
	}{
		{"empty", func(*Ring) {}},
		{"typical", func(r *Ring) { writeChunked(r, benchOutput) }},
		{"worst", func(r *Ring) {
			for range 2 * ringCapacity {
				r.Write(long)
			}
		}},
	}
	for _, c := range cases {
		b.Run(c.name, func(b *testing.B) {
			var perRing float64
			for b.Loop() {
				before := heapInUse()
				r := newBenchRing()
				c.fill(r)
				perRing = float64(heapInUse() - before)
				runtime.KeepAlive(r)
			}
			b.ReportMetric(perRing, "heap-B/ring")
		})
	}
}

func heapInUse() uint64 {
	runtime.GC()
	runtime.GC()
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	return ms.HeapInuse
}

func openFDs() int {
	entries, _ := os.ReadDir("/dev/fd")
	return len(entries)
}

func rusage() (cpu time.Duration, maxRSS int64) {
	var ru syscall.Rusage
	_ = syscall.Getrusage(syscall.RUSAGE_SELF, &ru)
	maxRSS = int64(ru.Maxrss) // bytes on darwin, KiB elsewhere
	if runtime.GOOS != "darwin" {
		maxRSS <<= 10
	}
	return time.Duration(ru.Utime.Nano() + ru.Stime.Nano()), maxRSS
}

// scaleN is how many terminals the scale benchmark spawns. macOS allows 511
// PTYs system-wide by default (kern.tty.ptmx_max), so it uses fewer there.
func scaleN() int {
	if n, err := strconv.Atoi(os.Getenv("PTYD_SCALE_N")); err == nil && n > 0 {
		return n
	}
	if runtime.GOOS == "darwin" {
		return 400
	}
	return 1000
}

// BenchmarkIdleTerminals spawns N idle `cat` terminals (half with a watch)
// and reports what each one costs while nothing happens: heap, peak RSS,
// goroutines, OS threads created, file descriptors, and process CPU time burned over a 2s
// idle window.
//
//	go test -run '^$' -bench IdleTerminals -benchtime 1x ./internal/terminal
func BenchmarkIdleTerminals(b *testing.B) {
	n := scaleN()
	for b.Loop() {
		m := NewManager()
		threads := pprof.Lookup("threadcreate")
		heap0, g0, th0 := heapInUse(), runtime.NumGoroutine(), threads.Count()
		_, rss0 := rusage()
		fd0 := openFDs()
		for i := range n {
			cfg := CreateConfig{Name: fmt.Sprintf("idle-%d", i), Command: "cat"}
			if i%2 == 0 {
				cfg.Watches = []WatchSpec{{Name: "err", Pattern: "error"}}
			}
			if _, err := m.Create(cfg); err != nil {
				b.Fatalf("create %d: %v", i, err)
			}
		}
		time.Sleep(500 * time.Millisecond) // let start-up settle
		heap1, g1, th1, fd1 := heapInUse(), runtime.NumGoroutine(), threads.Count(), openFDs()
		cpu0, rss1 := rusage()
		time.Sleep(2 * time.Second)
		cpu1, _ := rusage()
		idle := cpu1 - cpu0
		b.ReportMetric(float64(heap1-heap0)/float64(n), "heap-B/term")
		b.ReportMetric(float64(rss1-rss0)/float64(n), "maxrss-B/term")
		b.ReportMetric(float64(g1-g0)/float64(n), "goroutines/term")
		b.ReportMetric(float64(th1-th0)/float64(n), "threads/term")
		b.ReportMetric(float64(fd1-fd0)/float64(n), "fds/term")
		b.ReportMetric(float64(idle.Microseconds())/1000/2, "idle-cpu-ms/s")
		b.ReportMetric(float64(n), "terminals")
		m.CloseAll()
	}
}
