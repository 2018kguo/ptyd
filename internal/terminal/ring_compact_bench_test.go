package terminal

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"slices"
	"testing"
	"time"
	"unsafe"
)

// TestRingColdWitness is an opt-in fresh-process retained-heap measurement.
// Build with go test -c, then run the binary with PTYD_RING_WITNESS set to
// prompt, burst, or repeat; PTYD_RING_COMPACT=1 enables cold reclamation.
// It also records the first resumed write and compaction's whole lock hold.
func TestRingColdWitness(t *testing.T) {
	mode := os.Getenv("PTYD_RING_WITNESS")
	if mode == "" {
		t.Skip("set PTYD_RING_WITNESS for a fresh-process measurement")
	}
	if mode != "prompt" && mode != "burst" && mode != "repeat" {
		t.Fatalf("unknown witness %q", mode)
	}
	compact := os.Getenv("PTYD_RING_COMPACT") == "1"
	count := 32
	if mode == "prompt" {
		// Tiny rings need a larger sample to amortize test-runtime noise.
		count = 4096
	}
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	rings := make([]*Ring, count)
	compactTimes := make([]int64, 0, count)
	for i := range rings {
		r := newBenchRing()
		rounds := 0
		if mode == "burst" {
			rounds = 1
		} else if mode == "repeat" {
			rounds = 4
		}
		for range rounds {
			fillColdRing(r, ringCapacity)
			r.Write([]byte("prompt$ "))
			if compact {
				started := time.Now()
				r.Compact()
				compactTimes = append(compactTimes, time.Since(started).Nanoseconds())
			}
		}
		if rounds == 0 {
			r.Write([]byte("prompt$ "))
		}
		rings[i] = r
	}
	runtime.GC()
	runtime.GC()
	runtime.ReadMemStats(&after)
	var slotBytes, pendingBytes, retainedBytes, lines int
	for _, r := range rings {
		slotBytes += len(r.lines) * int(unsafe.Sizeof(Line{}))
		pendingBytes += cap(r.pending)
		retainedBytes += r.bytes
		lines += r.n
	}
	resumeTimes := make([]int64, 0, count)
	for _, r := range rings {
		started := time.Now()
		r.Write([]byte("continued"))
		resumeTimes = append(resumeTimes, time.Since(started).Nanoseconds())
	}
	percentile := func(samples []int64, percent int) int64 {
		if len(samples) == 0 {
			return 0
		}
		slices.Sort(samples)
		return samples[(len(samples)-1)*percent/100]
	}
	body, err := json.Marshal(struct {
		Mode                 string `json:"mode"`
		Compact              bool   `json:"compact"`
		Rings                int    `json:"rings"`
		HeapBytesPerRing     int64  `json:"heap_bytes_per_ring"`
		HeapInusePerRing     int64  `json:"heap_inuse_per_ring"`
		SlotBytesPerRing     int    `json:"slot_bytes_per_ring"`
		PendingBytesPerRing  int    `json:"pending_bytes_per_ring"`
		RetainedBytesPerRing int    `json:"retained_bytes_per_ring"`
		LinesPerRing         int    `json:"lines_per_ring"`
		ResumeP50NS          int64  `json:"resume_p50_ns"`
		ResumeP95NS          int64  `json:"resume_p95_ns"`
		ResumeP99NS          int64  `json:"resume_p99_ns"`
		CompactP50NS         int64  `json:"compact_p50_ns"`
		CompactP99NS         int64  `json:"compact_p99_ns"`
	}{
		Mode: mode, Compact: compact, Rings: count,
		HeapBytesPerRing: int64(after.HeapAlloc-before.HeapAlloc) / int64(count),
		HeapInusePerRing: int64(after.HeapInuse-before.HeapInuse) / int64(count),
		SlotBytesPerRing: slotBytes / count, PendingBytesPerRing: pendingBytes / count,
		RetainedBytesPerRing: retainedBytes / count, LinesPerRing: lines / count,
		ResumeP50NS: percentile(resumeTimes, 50), ResumeP95NS: percentile(resumeTimes, 95), ResumeP99NS: percentile(resumeTimes, 99),
		CompactP50NS: percentile(compactTimes, 50), CompactP99NS: percentile(compactTimes, 99),
	})
	if err != nil {
		t.Fatal(err)
	}
	fmt.Println(string(body))
	runtime.KeepAlive(rings)
}

func BenchmarkRingColdResume(b *testing.B) {
	for _, size := range []int{9, readBufSize} {
		for _, compact := range []bool{false, true} {
			b.Run(fmt.Sprintf("bytes=%d/compact=%t", size, compact), func(b *testing.B) {
				r := newBenchRing()
				fillColdRing(r, ringCapacity)
				r.Write([]byte("prompt$ "))
				if compact {
					r.Compact()
				}
				pending := r.pending
				chunk := bytes.Repeat([]byte{'x'}, size)
				b.ReportAllocs()
				for b.Loop() {
					// Restore the cold backing slice: otherwise the first large
					// write would grow it and hide subsequent resume allocations.
					r.pending = pending
					r.Write(chunk)
				}
			})
		}
	}
}
