package terminal

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/2018kguo/phosphor"
	"github.com/2018kguo/ptyd/internal/benchdata"
)

// fakeTerminal registers a terminal with no process behind it, for driving
// the output path (consume) and the read-side API directly. It must not be
// closed through the manager.
func fakeTerminal(m *Manager, i, cols, rows int) *terminal {
	id := ID(fmt.Sprintf("term-%d-fake", i))
	t := &terminal{
		mgr: m,
		info: Info{ID: id, Name: fmt.Sprintf("fake-%d", i), Status: StatusRunning, CreatedAt: time.Unix(int64(i), 0),
			Tags: []string{"t" + strconv.Itoa(i%10)}},
		ring:   NewRing(ringCapacity, m.opts.ScrollbackBytes),
		screen: phosphor.New(cols, rows),
		done:   make(chan struct{}),
	}
	m.mu.Lock()
	m.terms[id] = t
	m.mu.Unlock()
	return t
}

func consumeChunked(t *terminal, out []byte) {
	for len(out) > 0 {
		n := min(len(out), readBufSize)
		t.consume(out[:n])
		out = out[n:]
	}
}

var hotStreams = []struct {
	name string
	data []byte
}{
	{"claude", benchdata.Claude(1<<20, 120, 40)},
	{"logs", benchdata.Logs(1 << 20)},
	{"progress", benchdata.Progress(1 << 20)},
}

// BenchmarkConsume is the whole per-read output path of one terminal
// (screen, attachers, tail, ring, counters) per MiB of output.
func BenchmarkConsume(b *testing.B) {
	for _, s := range hotStreams {
		b.Run(s.name, func(b *testing.B) {
			t := fakeTerminal(NewManager(), 0, 120, 40)
			b.SetBytes(int64(len(s.data)))
			b.ReportAllocs()
			for b.Loop() {
				consumeChunked(t, s.data)
			}
		})
	}
	// With persistence: every read is also appended to the tail, and
	// checkpoints are taken as the tail fills.
	b.Run("claude-persist", func(b *testing.B) {
		m := NewManagerWithOptions(Options{PersistDir: b.TempDir()})
		t := fakeTerminal(m, 0, 120, 40)
		dir := terminalDir(m.opts.PersistDir, t.info.ID)
		_ = os.MkdirAll(dir, 0o700)
		t.persist = &persister{dir: dir}
		data := hotStreams[0].data
		b.SetBytes(int64(len(data)))
		b.ReportAllocs()
		for b.Loop() {
			consumeChunked(t, data)
		}
		b.StopTimer()
		t.stopPersisting()
	})
}

// BenchmarkAttachFanout is one terminal's output path with K attached
// clients, each drained by its own reader.
func BenchmarkAttachFanout(b *testing.B) {
	data := hotStreams[0].data
	for _, k := range []int{1, 8, 64} {
		b.Run("K="+strconv.Itoa(k), func(b *testing.B) {
			m := NewManager()
			t := fakeTerminal(m, 0, 120, 40)
			ctx, cancel := context.WithCancel(context.Background())
			var wg sync.WaitGroup
			for range k {
				at, err := m.Attach(t.info.ID, 0, 0, true)
				if err != nil {
					b.Fatal(err)
				}
				wg.Go(func() {
					defer at.Close()
					for {
						if _, err := at.Read(ctx); err != nil {
							return
						}
					}
				})
			}
			b.SetBytes(int64(len(data)))
			b.ReportAllocs()
			for b.Loop() {
				consumeChunked(t, data)
			}
			b.StopTimer()
			cancel()
			wg.Wait()
		})
	}
}

// persistTerminal returns a fake terminal with persistence whose screen
// and scrollback are full of agent output.
func persistTerminal(b *testing.B) *terminal {
	m := NewManagerWithOptions(Options{PersistDir: b.TempDir()})
	t := fakeTerminal(m, 0, 120, 40)
	dir := terminalDir(m.opts.PersistDir, t.info.ID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		b.Fatal(err)
	}
	t.screen.Write(benchdata.Claude(4<<20, 120, 40))
	t.persist = &persister{dir: dir}
	return t
}

// BenchmarkCheckpoint is one checkpoint of a full screen and scrollback:
// repaint, atomic write with fsync, dropping the old tail.
func BenchmarkCheckpoint(b *testing.B) {
	t := persistTerminal(b)
	b.ReportAllocs()
	for b.Loop() {
		t.checkpoint()
	}
	b.StopTimer()
	fi, err := os.Stat(filepath.Join(t.persist.dir, ckptFile))
	if err != nil {
		b.Fatal(err)
	}
	b.ReportMetric(float64(fi.Size()), "ckpt-B")
}

// BenchmarkTailAppend is appending output to the tail file in PTY-read
// sized records, per MiB.
func BenchmarkTailAppend(b *testing.B) {
	p := &persister{dir: b.TempDir()}
	data := hotStreams[0].data
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	for b.Loop() {
		for out := data; len(out) > 0; {
			n := min(len(out), readBufSize)
			if p.appendLocked(recData, out[:n]) {
				// What a checkpoint does to the tail, without the checkpoint.
				p.tail.close()
				p.gen++
				p.tail, p.tailBytes = nil, 0
			}
			out = out[n:]
		}
	}
	b.StopTimer()
	p.tail.close()
}

// writeGhost writes a persisted terminal as a crashed daemon leaves it: a
// checkpoint of a full screen and scrollback plus tailBytes of output in
// the tail.
func writeGhost(b *testing.B, persistDir string, i int, ckpt []byte, tail []byte) ID {
	id := ID(fmt.Sprintf("term-%d-beef", i))
	dir := terminalDir(persistDir, id)
	if err := writeMeta(persistDir, persistedMeta{ID: id, Name: "g", Command: "sh", Cols: 120, Rows: 40, CreatedAt: time.Now()}); err != nil {
		b.Fatal(err)
	}
	if err := writeCheckpoint(dir, 120, 40, 1, ckpt); err != nil {
		b.Fatal(err)
	}
	if len(tail) > 0 {
		var rec []byte
		for out := tail; len(out) > 0; {
			n := min(len(out), readBufSize)
			rec = append(rec, recData, byte(n>>24), byte(n>>16), byte(n>>8), byte(n))
			rec = append(rec, out[:n]...)
			out = out[n:]
		}
		if err := os.WriteFile(filepath.Join(dir, tailPrefix+"1"), rec, 0o600); err != nil {
			b.Fatal(err)
		}
	}
	return id
}

// BenchmarkLoadScreen is rebuilding one terminal's screen after a crash:
// a full checkpoint plus a tail of the given size.
func BenchmarkLoadScreen(b *testing.B) {
	full := phosphor.New(120, 40)
	full.Write(benchdata.Claude(4<<20, 120, 40))
	ckpt := full.Snapshot()
	for _, tailBytes := range []int{0, 1 << 20, maxTailBytes} {
		b.Run("tail="+strconv.Itoa(tailBytes>>10)+"KiB", func(b *testing.B) {
			pd := b.TempDir()
			id := writeGhost(b, pd, 1, ckpt, benchdata.Claude(tailBytes, 120, 40))
			dir := terminalDir(pd, id)
			b.SetBytes(int64(len(ckpt) + tailBytes))
			b.ReportAllocs()
			for b.Loop() {
				if _, _, err := loadScreen(dir, 120, 40); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkStartup1000 is a daemon start over 1000 persisted terminals:
// pruning and loading the ghosts, then rebuilding every saved screen
// (checkpoint plus a 64 KiB tail), as listing their screens or restoring
// them all does.
func BenchmarkStartup1000(b *testing.B) {
	const n = 1000
	full := phosphor.New(120, 40)
	full.Write(benchdata.Claude(1<<20, 120, 40))
	ckpt := full.Snapshot()
	tail := benchdata.Claude(64<<10, 120, 40)
	pd := b.TempDir()
	for i := range n {
		writeGhost(b, pd, i+1, ckpt, tail)
	}
	b.Run("Manager", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if m := NewManagerWithOptions(Options{PersistDir: pd}); len(m.ghosts) != n {
				b.Fatalf("%d ghosts", len(m.ghosts))
			}
		}
	})
	b.Run("Screens", func(b *testing.B) {
		m := NewManagerWithOptions(Options{PersistDir: pd})
		b.ReportAllocs()
		for b.Loop() {
			for id := range m.ghosts {
				if _, ok := m.loadGhostScreen(id); !ok {
					b.Fatal("no screen")
				}
			}
		}
	})
}

// scaleManager is a manager with n fake terminals that each hold lines
// lines of output.
func scaleManager(n, lines int) *Manager {
	m := NewManager()
	out := benchdata.Logs(lines * 110)
	for i := range n {
		t := fakeTerminal(m, i, 80, 24)
		t.ring.Write(out)
		t.lastOutputAt = time.Now()
	}
	return m
}

// BenchmarkManagerScale is the read side of the API against N terminals.
func BenchmarkManagerScale(b *testing.B) {
	for _, n := range []int{100, 1000, 5000} {
		m := scaleManager(n, 200)
		var some ID
		for id := range m.terms {
			some = id
			break
		}
		b.Run(fmt.Sprintf("List/N=%d", n), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				m.List()
			}
		})
		b.Run(fmt.Sprintf("Metrics/N=%d", n), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				m.Metrics()
			}
		})
		b.Run(fmt.Sprintf("Get/N=%d", n), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if _, err := m.Get(some); err != nil {
					b.Fatal(err)
				}
			}
		})
		b.Run(fmt.Sprintf("Search/N=%d", n), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if _, err := m.Search(`status=500`, 0, 10, nil); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkRingSubscribe is what a stream connect costs against a full
// ring, when the client only wants what comes next.
func BenchmarkRingSubscribe(b *testing.B) {
	r := fullBenchRing()
	b.ReportAllocs()
	buf := make([]Line, 256)
	for b.Loop() {
		c := Cursor{ring: r, last: r.LatestSeq()}
		c.Read(buf)
	}
}

// BenchmarkWaitRetained measures an agent waiting for a marker after a full
// retained output window, with and without preceding context.
func BenchmarkWaitRetained(b *testing.B) {
	for _, before := range []int{0, 3, 256} {
		b.Run(fmt.Sprintf("Context=%d", before), func(b *testing.B) {
			m := NewManager()
			t := fakeTerminal(m, 0, 80, 24)
			for range ringCapacity - 1 {
				t.ring.Write([]byte("ordinary output\n"))
			}
			t.ring.Write([]byte("MATCH\n"))
			b.ReportAllocs()
			for b.Loop() {
				line, context, err := m.Wait(context.Background(), t.info.ID, "^MATCH$", -1, before)
				if err != nil || line.Text != "MATCH" || len(context) != before {
					b.Fatalf("wait: line=%+v context=%d err=%v", line, len(context), err)
				}
			}
		})
	}
}

// BenchmarkRestoreGhosts is a daemon start that restores 100 terminals
// with the rerun policy (each: rebuild the screen, stop the old process
// group, start the command, checkpoint the restored screen).
func BenchmarkRestoreGhosts(b *testing.B) {
	const n = 100
	pd := b.TempDir()
	m := NewManagerWithOptions(Options{PersistDir: pd})
	for i := range n {
		if _, err := m.Create(CreateConfig{Name: fmt.Sprintf("r%d", i), Command: "cat", Restore: RestoreRerun}); err != nil {
			b.Fatal(err)
		}
	}
	m.CloseAll()
	for b.Loop() {
		m := NewManagerWithOptions(Options{PersistDir: pd})
		m.RestoreGhosts()
		b.StopTimer()
		if live := m.Metrics().LiveTerminals; live != n {
			b.Fatalf("%d restored", live)
		}
		m.CloseAll()
		b.StartTimer()
	}
}
