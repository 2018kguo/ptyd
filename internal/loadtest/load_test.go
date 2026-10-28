//go:build loadtest

// Package loadtest runs the daemon end to end under load: N terminals run
// a producer printing agent-TUI output at a fixed rate (or flat out)
// while clients attach to and stream each of them, and one more terminal
// echoes keystrokes typed through an attach connection. It reports
// keystroke echo latency, throughput, CPU, peak RSS and GC pauses, and can
// write CPU, mutex, block and heap profiles and an execution trace.
//
//	go test -tags loadtest -run Load -v -timeout 10m ./internal/loadtest
//
// Settings (environment):
//
//	PTYD_LOAD_N         producer terminals (default 32)
//	PTYD_LOAD_RATE      bytes/s each producer prints, 0 = flat out (default 1048576)
//	PTYD_LOAD_BURST     if set, print in bursts: PTYD_LOAD_BURST seconds of output at once, then as long quiet
//	PTYD_LOAD_LABEL     label recorded in the result
//	PTYD_LOAD_SECONDS   measured duration (default 10)
//	PTYD_LOAD_ATTACH    attached clients per producer terminal (default 1)
//	PTYD_LOAD_STREAMS   SSE clients per producer terminal (default 1)
//	PTYD_LOAD_PERSIST   1 to run with a persist dir (default 1)
//	PTYD_LOAD_PROFILE   directory to write cpu/mutex/block/heap profiles and a trace to
//	PTYD_LOAD_OUT       file to append the one-line JSON result to
//
// The daemon runs in this process (so the profiles and GC numbers are its
// own, plus the clients'); producers are child processes.
package loadtest

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"runtime/metrics"
	"runtime/pprof"
	"runtime/trace"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/2018kguo/ptyd/internal/api"
	"github.com/2018kguo/ptyd/internal/benchdata"
	"github.com/2018kguo/ptyd/internal/terminal"
)

func TestMain(m *testing.M) {
	if r := os.Getenv("PTYD_LOAD_PRODUCER"); r != "" {
		rate, _ := strconv.Atoi(r)
		burst, _ := strconv.Atoi(os.Getenv("PTYD_LOAD_BURST"))
		produce(rate, burst)
		return
	}
	os.Exit(m.Run())
}

// produce prints agent output to stdout at rate bytes/s (0: flat out)
// until killed. With burst > 0 it prints burst seconds' worth at once,
// then pauses for burst seconds.
func produce(rate, burst int) {
	data := benchdata.Claude(1<<20, 120, 40)
	const chunk = 4096
	if burst > 0 {
		for i := 0; ; {
			for n := 0; n < rate*burst; n += chunk {
				if _, err := os.Stdout.Write(data[i : i+chunk]); err != nil {
					return
				}
				i = (i + chunk) % (len(data) - chunk)
			}
			time.Sleep(time.Duration(burst) * time.Second)
		}
	}
	start := time.Now()
	sent := 0
	for i := 0; ; i = (i + chunk) % (len(data) - chunk) {
		if _, err := os.Stdout.Write(data[i : i+chunk]); err != nil {
			return
		}
		sent += chunk
		if rate > 0 {
			if ahead := time.Duration(float64(sent)/float64(rate)*float64(time.Second)) - time.Since(start); ahead > 0 {
				time.Sleep(ahead)
			}
		}
	}
}

func envInt(name string, def int) int {
	if v, err := strconv.Atoi(os.Getenv(name)); err == nil {
		return v
	}
	return def
}

// Result is one load-test run.
type Result struct {
	Label                    string
	N, Rate, Attach, Streams int
	Burst                    int
	Persist                  bool
	Seconds                  float64
	EchoP50Ms, EchoP99Ms     float64
	EchoMaxMs                float64
	Keystrokes               int
	OutputMBps               float64 // PTY output processed by the daemon
	AttachMBps               float64 // delivered to attached clients
	SSELinesPerSec           float64
	CPUCores                 float64 // process CPU time / wall time
	PeakRSSMiB               float64
	GCPauseP50Us             float64 // stop-the-world pauses (histogram bucket bounds)
	GCPauseP99Us             float64
	GCPauseMaxUs             float64
	GCCycles                 uint64
	HeapMiB                  float64
	// Disk writes during the measured window: write amplification is
	// persisted bytes per byte of output.
	WriteAmplification float64
	FsyncsPerSec       float64
	CheckpointsPerSec  float64
}

var errLoadStopped = errors.New("load measurement complete")

// Only our explicit teardown may close a healthy reader. EOF and unrelated
// errors remain failures even when they race the end of the measurement.
func loadReadError(ctx context.Context, err error) error {
	if context.Cause(ctx) == errLoadStopped &&
		(errors.Is(err, net.ErrClosed) || errors.Is(err, context.Canceled) || errors.Is(err, errLoadStopped) || errors.Is(err, http.ErrBodyReadAfterClose)) {
		return nil
	}
	if err == nil {
		return io.EOF
	}
	return err
}

func drainAttach(ctx context.Context, conn net.Conn, br io.Reader, count *atomic.Int64) error {
	stopClose := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stopClose()
	defer conn.Close()
	buf := make([]byte, 64<<10)
	for {
		n, err := br.Read(buf)
		count.Add(int64(n))
		if err != nil {
			return loadReadError(ctx, err)
		}
	}
}

func drainSSE(ctx context.Context, resp *http.Response, count *atomic.Int64) error {
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("SSE returned HTTP %d", resp.StatusCode)
	}
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		if string(sc.Bytes()) == "event: line" {
			count.Add(1)
		}
	}
	return loadReadError(ctx, sc.Err())
}

func loadClientCounts(counters []atomic.Int64) []int64 {
	out := make([]int64, len(counters))
	for i := range counters {
		out[i] = counters[i].Load()
	}
	return out
}

func loadDelivered(before, after []int64) (int64, error) {
	var total int64
	for i, count := range after {
		delta := count - before[i]
		if delta <= 0 {
			return 0, fmt.Errorf("client %d delivered no output in the measured window; increase duration for low rates", i)
		}
		total += delta
	}
	return total, nil
}

func TestLoad(t *testing.T) {
	cfg := Result{
		Label: os.Getenv("PTYD_LOAD_LABEL"),
		N:     envInt("PTYD_LOAD_N", 32), Rate: envInt("PTYD_LOAD_RATE", 1<<20),
		Burst:  envInt("PTYD_LOAD_BURST", 0),
		Attach: envInt("PTYD_LOAD_ATTACH", 1), Streams: envInt("PTYD_LOAD_STREAMS", 1),
		Persist: envInt("PTYD_LOAD_PERSIST", 1) == 1,
	}
	seconds := envInt("PTYD_LOAD_SECONDS", 10)
	dur := time.Duration(seconds) * time.Second
	if cfg.N < 1 || cfg.Rate < 0 || cfg.Burst < 0 || cfg.Attach < 0 || cfg.Streams < 0 || dur <= 0 {
		t.Fatal("load requires positive N/SECONDS and nonnegative RATE/BURST/ATTACH/STREAMS")
	}
	if cfg.Burst > 0 && (cfg.Rate == 0 || cfg.Burst > seconds/2) {
		t.Fatal("burst load requires positive RATE and SECONDS covering at least two BURST intervals")
	}

	opts := terminal.Options{}
	if cfg.Persist {
		opts.PersistDir = t.TempDir()
	}
	mgr := terminal.NewManagerWithOptions(opts)
	defer mgr.CloseAll()
	srv := api.New(mgr, api.Config{})
	ln, err := net.Listen("tcp", "127.0.0.1:0") // a free port the OS picks
	if err != nil {
		t.Fatal(err)
	}
	go srv.Serve(ln)
	defer srv.Shutdown(context.Background())
	addr := ln.Addr().String()

	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancelCause(context.Background())
	attachCounts := make([]atomic.Int64, cfg.N*cfg.Attach)
	sseCounts := make([]atomic.Int64, cfg.N*cfg.Streams)
	var wg sync.WaitGroup
	stopClients := func() {
		cancel(errLoadStopped)
		wg.Wait()
	}
	defer stopClients()

	// Producers, each with its clients.
	for i := range cfg.N {
		info, err := mgr.Create(terminal.CreateConfig{
			Name: fmt.Sprintf("prod-%d", i), Command: self, Cols: 120, Rows: 40,
			Env: map[string]string{"PTYD_LOAD_PRODUCER": strconv.Itoa(cfg.Rate), "PTYD_LOAD_BURST": strconv.Itoa(cfg.Burst)},
		})
		if err != nil {
			t.Fatal(err)
		}
		for client := range cfg.Attach {
			conn, br := dialAttach(t, addr, info.ID, true)
			wg.Go(func() {
				if err := drainAttach(ctx, conn, br, &attachCounts[i*cfg.Attach+client]); err != nil {
					t.Errorf("attach %s/%d ended: %v", info.ID, client, err)
				}
			})
		}
		for client := range cfg.Streams {
			req, _ := http.NewRequestWithContext(ctx, "GET", "http://"+addr+"/terminals/"+string(info.ID)+"/stream", nil)
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			wg.Go(func() {
				if err := drainSSE(ctx, resp, &sseCounts[i*cfg.Streams+client]); err != nil {
					t.Errorf("SSE %s/%d ended: %v", info.ID, client, err)
				}
			})
		}
	}

	// The echo terminal: a program that writes back every byte it reads.
	echo, err := mgr.Create(terminal.CreateConfig{Name: "echo", Command: "sh", Args: []string{"-c", "stty raw -echo; exec cat"}})
	if err != nil {
		t.Fatal(err)
	}
	econn, ebr := dialAttach(t, addr, echo.ID, false)
	defer econn.Close()
	received := make(chan byte, 1024)
	wg.Go(func() {
		defer close(received)
		stopClose := context.AfterFunc(ctx, func() { _ = econn.Close() })
		defer stopClose()
		buf := make([]byte, 4096)
		for {
			n, err := ebr.Read(buf)
			for _, c := range buf[:n] {
				select {
				case received <- c:
				default:
				}
			}
			if err != nil {
				if err := loadReadError(ctx, err); err != nil {
					t.Errorf("echo attachment ended: %v", err)
				}
				return
			}
		}
	})
	key := func(c byte) time.Duration {
		start := time.Now()
		if _, err := econn.Write(api.AppendFrame(nil, api.FrameInput, []byte{c})); err != nil {
			t.Fatal(err)
		}
		timeout := time.After(5 * time.Second)
		for {
			select {
			case got, ok := <-received:
				if !ok {
					t.Fatal("echo terminal ended")
				}
				if got == c {
					return time.Since(start)
				}
			case <-timeout:
				t.Fatalf("no echo of %q in 5s", c)
			}
		}
	}
	// Wait until the echo program is up (and skip the repaint).
	for deadline := time.Now().Add(5 * time.Second); ; {
		if key('!') < time.Second || time.Now().After(deadline) {
			break
		}
	}

	time.Sleep(time.Second) // let the producers reach steady state
	if t.Failed() {
		t.Fatal("load clients failed during warmup")
	}
	stopProfiles := startProfiles(t)
	runtime.GC()
	ms0 := readMetrics()
	cpu0 := cpuTime()
	met0 := mgr.Metrics()
	out0 := met0.BytesEmitted
	att0, sse0 := loadClientCounts(attachCounts), loadClientCounts(sseCounts)
	start := time.Now()

	var lat []time.Duration
	for i := 0; time.Since(start) < dur; i++ {
		lat = append(lat, key(byte('a'+i%26)))
		time.Sleep(5 * time.Millisecond)
	}

	elapsed := time.Since(start).Seconds()
	cfg.Seconds = elapsed
	cfg.CPUCores = (cpuTime() - cpu0).Seconds() / elapsed
	met := mgr.Metrics()
	cfg.OutputMBps = float64(met.BytesEmitted-out0) / elapsed / 1e6
	att, err := loadDelivered(att0, loadClientCounts(attachCounts))
	if err != nil {
		t.Errorf("attach: %v", err)
	}
	sse, err := loadDelivered(sse0, loadClientCounts(sseCounts))
	if err != nil {
		t.Errorf("SSE: %v", err)
	}
	if met.BytesEmitted-out0 <= int64(len(lat)) {
		t.Error("no producer output processed in the measured window; increase duration for low rates")
	}
	if running := met.PerStatus[string(terminal.StatusRunning)]; running != cfg.N+1 {
		t.Errorf("load lost producers: %d running terminals, want %d", running, cfg.N+1)
	}
	cfg.AttachMBps = float64(att) / elapsed / 1e6
	cfg.SSELinesPerSec = float64(sse) / elapsed
	ms1 := readMetrics()
	cfg.GCPauseP50Us = pauseQuantile(ms0.pauses, ms1.pauses, 0.5) * 1e6
	cfg.GCPauseP99Us = pauseQuantile(ms0.pauses, ms1.pauses, 0.99) * 1e6
	cfg.GCPauseMaxUs = pauseQuantile(ms0.pauses, ms1.pauses, 1) * 1e6
	cfg.GCCycles = ms1.cycles - ms0.cycles
	cfg.HeapMiB = float64(ms1.heap) / (1 << 20)
	stopProfiles()
	cfg.PeakRSSMiB = peakRSS() / (1 << 20)
	slices.Sort(lat)
	q := func(p float64) float64 {
		return float64(lat[min(int(p*float64(len(lat))), len(lat)-1)].Microseconds()) / 1000
	}
	cfg.Keystrokes = len(lat)
	cfg.EchoP50Ms, cfg.EchoP99Ms, cfg.EchoMaxMs = q(0.5), q(0.99), q(1)
	if out := met.BytesEmitted - out0; out > 0 {
		cfg.WriteAmplification = float64(met.PersistBytes-met0.PersistBytes) / float64(out)
	}
	cfg.FsyncsPerSec = float64(met.Fsyncs-met0.Fsyncs) / elapsed
	cfg.CheckpointsPerSec = float64(met.Checkpoints-met0.Checkpoints) / elapsed
	stopClients()
	if t.Failed() {
		t.Fatal("load clients failed; discarding measurement")
	}

	line, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%s", line)
	if path := os.Getenv("PTYD_LOAD_OUT"); path != "" {
		f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			t.Fatal(err)
		}
		_, err = f.Write(append(line, '\n'))
		if err = errors.Join(err, f.Close()); err != nil {
			t.Fatal(err)
		}
	}
}

// dialAttach opens an attach connection and returns it after the 101.
func dialAttach(t *testing.T, addr string, id terminal.ID, readOnly bool) (net.Conn, *bufio.Reader) {
	t.Helper()
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	q := "cols=120&rows=40"
	if readOnly {
		q += "&readonly=1"
	}
	fmt.Fprintf(conn, "GET /terminals/%s/attach?%s HTTP/1.1\r\nHost: %s\r\nConnection: Upgrade\r\nUpgrade: %s\r\n\r\n", id, q, addr, api.AttachProtocol)
	br := bufio.NewReaderSize(conn, 64<<10)
	resp, err := http.ReadResponse(br, nil)
	if err != nil || resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("attach %s: %v %v", id, resp, err)
	}
	return conn, br
}

// startProfiles starts the profiles PTYD_LOAD_PROFILE asks for and
// returns the function that writes them.
func startProfiles(t *testing.T) func() {
	dir := os.Getenv("PTYD_LOAD_PROFILE")
	if dir == "" {
		return func() {}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	create := func(name string) *os.File {
		f, err := os.Create(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		return f
	}
	runtime.SetMutexProfileFraction(10)
	runtime.SetBlockProfileRate(int(10 * time.Microsecond))
	cpu := create("cpu.pprof")
	if err := pprof.StartCPUProfile(cpu); err != nil {
		t.Fatal(err)
	}
	tr := create("trace.out")
	if err := trace.Start(tr); err != nil {
		t.Fatal(err)
	}
	return func() {
		trace.Stop()
		tr.Close()
		pprof.StopCPUProfile()
		cpu.Close()
		for _, p := range []string{"mutex", "block", "heap", "allocs"} {
			f := create(p + ".pprof")
			_ = pprof.Lookup(p).WriteTo(f, 0)
			f.Close()
		}
		runtime.SetMutexProfileFraction(0)
		runtime.SetBlockProfileRate(0)
	}
}

type rtMetrics struct {
	pauses *metrics.Float64Histogram
	cycles uint64
	heap   uint64
}

func readMetrics() rtMetrics {
	s := []metrics.Sample{
		{Name: "/sched/pauses/total/gc:seconds"},
		{Name: "/gc/cycles/total:gc-cycles"},
		{Name: "/memory/classes/heap/objects:bytes"},
	}
	metrics.Read(s)
	return rtMetrics{pauses: s[0].Value.Float64Histogram(), cycles: s[1].Value.Uint64(), heap: s[2].Value.Uint64()}
}

// pauseQuantile returns the q quantile of the GC pauses between two
// histogram readings (upper bucket bound, seconds).
func pauseQuantile(a, b *metrics.Float64Histogram, q float64) float64 {
	var total uint64
	d := make([]uint64, len(b.Counts))
	for i := range b.Counts {
		d[i] = b.Counts[i] - a.Counts[i]
		total += d[i]
	}
	if total == 0 {
		return 0
	}
	want := uint64(math.Ceil(q * float64(total)))
	var acc uint64
	for i, c := range d {
		acc += c
		if acc >= want {
			return b.Buckets[i+1]
		}
	}
	return b.Buckets[len(b.Buckets)-1]
}

func cpuTime() time.Duration {
	var ru syscall.Rusage
	_ = syscall.Getrusage(syscall.RUSAGE_SELF, &ru)
	return time.Duration(ru.Utime.Nano() + ru.Stime.Nano())
}

func peakRSS() float64 {
	var ru syscall.Rusage
	_ = syscall.Getrusage(syscall.RUSAGE_SELF, &ru)
	if runtime.GOOS == "darwin" {
		return float64(ru.Maxrss)
	}
	return float64(ru.Maxrss) * 1024
}
