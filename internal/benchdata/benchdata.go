// Package benchdata generates deterministic terminal output streams for
// benchmarks and the load test: what an agent TUI, a log tail and a
// progress bar print. It is not used by the daemon.
package benchdata

import (
	"fmt"
	"strings"
)

// Claude is about size bytes shaped like Claude Code's output on a
// cols x rows terminal: synchronized updates that move the cursor up over
// the live region, erase and redraw each of its lines with truecolor and
// 256-color SGR runs, box drawing and a spinner, while finished message
// text scrolls into the scrollback above.
func Claude(size, cols, rows int) []byte {
	var b strings.Builder
	b.Grow(size + 4096)
	spin := []string{"✻", "✽", "✶", "✳", "✢", "·"}
	region := min(12, rows-2) // lines of the live region redrawn each frame
	words := strings.Fields("the quick brown fox jumps over the lazy dog while reading internal/terminal/manager.go and running go test ./... to check the ring buffer")
	drawn := 0
	for f := 0; b.Len() < size; f++ {
		b.WriteString("\x1b[?2026h")
		if drawn > 0 {
			// Ink erases the previous frame line by line, moving up.
			for range drawn {
				b.WriteString("\x1b[2K\x1b[1A")
			}
			b.WriteString("\x1b[2K\x1b[G")
		}
		// Every few frames a finished line of the message is committed
		// above the live region, so it scrolls into the scrollback.
		if f%3 == 0 {
			fmt.Fprintf(&b, "\x1b[38;2;%d;%d;%dm●\x1b[39m ", 120+f%100, 180, 255-f%100)
			for i := range 6 + f%7 {
				w := words[(f+i)%len(words)]
				switch i % 4 {
				case 1:
					fmt.Fprintf(&b, "\x1b[1m%s\x1b[22m ", w)
				case 2:
					fmt.Fprintf(&b, "\x1b[38;5;%dm%s\x1b[39m ", 16+(f+i)%216, w)
				default:
					b.WriteString(w + " ")
				}
			}
			b.WriteString("\r\n")
		}
		// The live region: a status line with a spinner, a boxed input
		// with a prompt, and hint lines, all heavily styled.
		bar := strings.Repeat("─", cols-2)
		fmt.Fprintf(&b, "\x1b[38;2;215;119;87m%s\x1b[39m \x1b[38;2;215;119;87mThinking…\x1b[39m \x1b[2m(%ds · ↑ %d tokens · esc to interrupt)\x1b[22m\r\n",
			spin[f%len(spin)], f/10, f*37%9000)
		lines := 1
		fmt.Fprintf(&b, "\x1b[38;2;136;136;136m╭%s╮\x1b[39m\r\n", bar)
		fmt.Fprintf(&b, "\x1b[38;2;136;136;136m│\x1b[39m > %s\x1b[7m \x1b[27m%s\x1b[38;2;136;136;136m│\x1b[39m\r\n",
			strings.Repeat("x", f%20), strings.Repeat(" ", max(cols-6-f%20, 0)))
		fmt.Fprintf(&b, "\x1b[38;2;136;136;136m╰%s╯\x1b[39m\r\n", bar)
		lines += 3
		for i := lines; i < region; i++ {
			fmt.Fprintf(&b, "  \x1b[2m\x1b[38;5;%dm? for shortcuts\x1b[39m\x1b[22m%s\x1b[38;2;%d;%d;%dm◯ auto-accept edits on\x1b[39m\r\n",
				244+i%8, strings.Repeat(" ", 20), 100+i, 200, 100+f%50)
			lines++
		}
		b.WriteString("\x1b[2K") // the cursor ends on a blank last line
		drawn = lines
		b.WriteString("\x1b[?2026l")
	}
	return []byte(b.String())
}

// Logs is about size bytes of colored, timestamped log lines.
func Logs(size int) []byte {
	var b strings.Builder
	b.Grow(size + 256)
	levels := []string{"\x1b[32mINFO\x1b[0m", "\x1b[33mWARN\x1b[0m", "\x1b[36mDEBUG\x1b[0m"}
	for i := 0; b.Len() < size; i++ {
		fmt.Fprintf(&b, "2026-10-21T03:%02d:%02d.%03dZ %s request id=%d path=/api/v1/items/%d status=200 took=%dms\r\n",
			i/60%60, i%60, i%1000, levels[i%3], i, i*7, i%97)
	}
	return []byte(b.String())
}

// Progress is about size bytes of carriage-return progress bars: each
// update redraws the same line, and only every 100% bar ends a line.
func Progress(size int) []byte {
	var b strings.Builder
	b.Grow(size + 256)
	for i := 0; b.Len() < size; i++ {
		for p := 0; p <= 100; p += 2 {
			fmt.Fprintf(&b, "\r\x1b[36mdownloading\x1b[0m layer %d [%-50s] %3d%% %d.%d MB/s", i, strings.Repeat("#", p/2), p, p%17, p%10)
		}
		b.WriteString("\r\n")
	}
	return []byte(b.String())
}
