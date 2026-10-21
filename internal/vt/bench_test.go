package vt

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func benchInput(b *testing.B) []byte {
	var all []byte
	paths, _ := filepath.Glob("testdata/*.raw")
	for _, p := range paths {
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
