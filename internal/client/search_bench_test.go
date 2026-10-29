package client

import (
	"context"
	"fmt"
	"github.com/2018kguo/phosphor"
	"github.com/2018kguo/ptyd/internal/terminal"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

var benchmarkSearchMatch *ContentMatch
var benchmarkSearchError error
var benchmarkSearchResult ContentSearch

func BenchmarkContentSearchBytes(b *testing.B) {
	for _, style := range []string{"hardBreaks", "openWrap"} {
		for _, k := range []int{64, 256, 1024} {
			b.Run(fmt.Sprintf("%s/%dKiB", style, k), func(b *testing.B) {
				v := phosphor.New(80, 24, phosphor.WithScrollback(20000), phosphor.WithScrollbackBytes(2<<20))
				input := strings.Repeat("abcdefgh01234567", k*1024/16)
				if style == "hardBreaks" {
					input = strings.Repeat(strings.Repeat("abcdefgh01234567", 5)+"\r\n", k*1024/82)
				}
				v.Write([]byte(input))
				row := terminal.SessionSummary{Name: "codex"}
				snapshot := v.Snapshot()
				_, preErr := MatchContents(row, v, "absent_needle")
				b.ReportAllocs()
				b.SetBytes(int64(len(input)))
				b.ResetTimer()
				for range b.N {
					benchmarkSearchMatch, benchmarkSearchError = MatchContents(row, v, "absent_needle")
				}
				b.ReportMetric(float64(len(snapshot)), "snapshot-B")
				if preErr != nil {
					b.ReportMetric(1, "partial/op")
				} else {
					b.ReportMetric(0, "partial/op")
				}
			})
		}
	}
}
func BenchmarkContentSearchSessions(b *testing.B) {
	v := phosphor.New(80, 24)
	v.Write([]byte("ready"))
	raw := v.Snapshot()
	for _, n := range []int{1, 8, 64, 500} {
		b.Run(fmt.Sprintf("sessions%d", n), func(b *testing.B) {
			var reads, conns atomic.Int64
			server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { reads.Add(1); w.Write(raw) }))
			server.Config.ConnState = func(_ net.Conn, state http.ConnState) {
				if state == http.StateNew {
					conns.Add(1)
				}
			}
			server.Start()
			defer server.Close()
			c := API{Addr: strings.TrimPrefix(server.URL, "http://")}
			o := terminal.Overview{Counts: terminal.OverviewCounts{Total: n}}
			for i := 0; i < n; i++ {
				o.Sessions = append(o.Sessions, terminal.SessionSummary{ID: terminal.ID(fmt.Sprintf("term-%d", i)), Name: "codex"})
			}
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				benchmarkSearchResult = c.SearchContents(context.Background(), o, "absent_needle")
				if !benchmarkSearchResult.Complete {
					b.Fatal(benchmarkSearchResult)
				}
			}
			b.StopTimer()
			if got := reads.Load(); got != int64(n*b.N) {
				b.Fatalf("requests=%d expected=%d", got, n*b.N)
			}
			b.ReportMetric(float64(n), "GET/op")
			b.ReportMetric(float64(conns.Load())/float64(b.N), "new-conns/op")
		})
	}
}
