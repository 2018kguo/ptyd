package vt

import (
	"os"
	"runtime"
	"testing"

	"github.com/2018kguo/ptyd/internal/benchdata"
)

func benchInput(b *testing.B) []byte {
	var all []byte
	// The original vim and less recordings only, so the benchmark input
	// stays the same as recordings are added.
	for _, p := range []string{"testdata/less.raw", "testdata/vim.raw"} {
		d, err := os.ReadFile(p)
		if err != nil {
			b.Fatal(err)
		}
		all = append(all, d...)
	}
	return all
}

// BenchmarkWrite feeds recorded vim and less output.
func BenchmarkWrite(b *testing.B) {
	in := benchInput(b)
	term := New(100, 30, Options{})
	b.SetBytes(int64(len(in)))
	b.ReportAllocs()
	for b.Loop() {
		term.Write(in)
	}
}

// BenchmarkWriteLines feeds plain scrolling log output, which exercises
// scrollback.
func BenchmarkWriteLines(b *testing.B) {
	var in []byte
	for range 1000 {
		in = append(in, "\x1b[32mINFO\x1b[0m request id=12345 path=/api/v1/items status=200 took=3ms\r\n"...)
	}
	term := New(120, 40, Options{})
	b.SetBytes(int64(len(in)))
	b.ReportAllocs()
	for b.Loop() {
		term.Write(in)
	}
}

func BenchmarkRepaint(b *testing.B) {
	term := New(100, 30, Options{})
	term.Write(benchInput(b))
	b.ReportAllocs()
	for b.Loop() {
		term.Repaint(true)
	}
}

// BenchmarkMemory reports the heap one terminal holds when idle at a
// prompt and when its screen and scrollback are full.
func BenchmarkMemory(b *testing.B) {
	full := func(t *Terminal) {
		for range 5000 {
			t.Write([]byte("\x1b[32mINFO\x1b[0m request id=12345 path=/api/v1/items status=200 took=3ms and some more text\r\n"))
		}
	}
	for _, c := range []struct {
		name string
		fill func(*Terminal)
	}{
		{"prompt-80x24", func(t *Terminal) { t.Write([]byte("user@host:~$ ")) }},
		{"full-80x24", full},
		{"full-200x50", full},
	} {
		b.Run(c.name, func(b *testing.B) {
			cols, rows := 80, 24
			if c.name == "full-200x50" {
				cols, rows = 200, 50
			}
			var per float64
			for b.Loop() {
				h0 := heapInUse()
				t := New(cols, rows, Options{})
				c.fill(t)
				per = float64(heapInUse() - h0)
				runtime.KeepAlive(t)
			}
			b.ReportMetric(per, "heap-B/term")
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

// streams are the realistic inputs: the recorded vim and less sessions,
// and generated agent-TUI, log and progress-bar output (~1 MiB each).
func streams(b *testing.B) []struct {
	name string
	data []byte
} {
	vim, err := os.ReadFile("testdata/vim.raw")
	if err != nil {
		b.Fatal(err)
	}
	less, err := os.ReadFile("testdata/less.raw")
	if err != nil {
		b.Fatal(err)
	}
	return []struct {
		name string
		data []byte
	}{
		{"vim", vim},
		{"less", less},
		{"claude", benchdata.Claude(1<<20, 120, 40)},
		{"logs", benchdata.Logs(1 << 20)},
		{"progress", benchdata.Progress(1 << 20)},
	}
}

// BenchmarkWriteStream feeds each stream in PTY-read-sized chunks, as the
// daemon does.
func BenchmarkWriteStream(b *testing.B) {
	for _, s := range streams(b) {
		b.Run(s.name, func(b *testing.B) {
			term := New(120, 40, Options{})
			b.SetBytes(int64(len(s.data)))
			b.ReportAllocs()
			for b.Loop() {
				for p := s.data; len(p) > 0; {
					n := min(len(p), 8192)
					term.Write(p[:n])
					p = p[n:]
				}
			}
		})
	}
}

// fullTerm is a 120x40 terminal with full scrollback of agent output.
func fullTerm() *Terminal {
	term := New(120, 40, Options{})
	term.Write(benchdata.Claude(4<<20, 120, 40))
	return term
}

// BenchmarkSerialize covers what the API and checkpoints read from a
// terminal with a full scrollback: the repaint with and without
// scrollback, and the screen and scrollback as text.
func BenchmarkSerialize(b *testing.B) {
	term := fullTerm()
	b.Run("RepaintScreen", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			term.Repaint(false)
		}
	})
	b.Run("RepaintScrollback", func(b *testing.B) {
		b.SetBytes(int64(len(term.Repaint(true))))
		b.ReportAllocs()
		for b.Loop() {
			term.Repaint(true)
		}
	})
	b.Run("Lines", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			term.Lines()
		}
	})
	b.Run("ScrollbackLines", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			term.ScrollbackLines()
		}
	})
}

// BenchmarkReplay is restoring a screen: a fresh emulator fed a full
// checkpoint (scrollback plus screen).
func BenchmarkReplay(b *testing.B) {
	ckpt := fullTerm().Repaint(true)
	b.SetBytes(int64(len(ckpt)))
	b.ReportAllocs()
	for b.Loop() {
		New(120, 40, Options{}).Write(ckpt)
	}
}
