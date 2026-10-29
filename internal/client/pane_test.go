package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/2018kguo/phosphor"
	"github.com/2018kguo/ptyd/internal/terminal"
)

func TestPaneKeepsStatusOutsideApp(t *testing.T) {
	writes := []string{
		"\x1b[2J\x1b[H\x1b[?2027h👩‍💻\x1b[?2027lX\r\n\x1b[?2027h☔︎1️⃣\x1b[?2027lZ",
		"\x1b[2J\x1b[H\x1b[48;2;48;52;60m\x1b[2K\x1b[1;3;4:3;38;2;180;150;230m composer \x1b[0m\r\n",
		"\x1b7\x1b[?1049h\x1b[2J\x1b[H\x1b[48;2;60;64;72m\x1b[2Keditor 雪👩‍💻\x1b[0m",
		"\x1b[?1049l\x1b8\x1b[2;20r\x1b[?6h\x1b[1;1Horigin\x1b[?6l\x1b[r",
		"\x1b[?2026h\x1b[2J\x1b[Hnew frame\x1b[?2026l\x1b[?2004h\x1b[?1004h",
		"\x1b[2J\x1b[H\x1b[?2027h\x1b[1;3H🇧🇨\x1b[?2027l\x1b[1;1H🇦",
		"\x1bcreset\x1b[23;1Hlast app row\x1b[?25l",
	}
	for cut := 0; cut < 30; cut++ {
		var wire bytes.Buffer
		p := newPane(&wire, "Codex · Review colors", 100, 24)
		host := phosphor.New(100, 24)
		for _, write := range writes {
			split := min(cut, len(write))
			for _, part := range []string{write[:split], write[split:]} {
				p.Write([]byte(part))
				p.draw(false)
				host.Write(wire.Bytes())
				wire.Reset()
			}
			if text := host.Line(23); !strings.Contains(text, "attached") || !strings.Contains(text, "Codex · Review colors") || !strings.Contains(text, "jk: back") {
				t.Fatalf("status lost: %q", text)
			}
			for y := 0; y < 23; y++ {
				for x := 0; x < 100; x++ {
					want, got := p.screen.Cell(x, y), host.Cell(x, y)
					if want.Rune == 0 {
						want.Rune = ' '
					}
					if got.Rune == 0 {
						got.Rune = ' '
					}
					if want.Width == 0 {
						continue
					}
					if got.Rune != want.Rune || got.Combining != want.Combining || got.Style != want.Style {
						t.Fatalf("cut%d at %d,%d got %+v want %+v", cut, x, y, got, want)
					}
				}
			}
			if want, got := p.screen.Cursor(), host.Cursor(); want.X != got.X || want.Y != got.Y || want.Visible != got.Visible {
				t.Fatalf("cursor got %+v want %+v", got, want)
			}
		}
	}
}
func TestFramedAttachReservesRowAcrossResize(t *testing.T) {
	mgr, addr := startDaemon(t)
	info, err := mgr.Create(terminal.CreateConfig{Name: "color-view", Command: "sh", Args: []string{"-c", `printf '\033[48;2;48;52;60m\033[2KINPUT\033[0m\r\n'; exec cat`}})
	if err != nil {
		t.Fatal(err)
	}
	u := newUserTerminal(t, 80, 24)
	winch := make(chan os.Signal, 1)
	done := make(chan result, 1)
	go func() {
		r, e := Attach(context.Background(), Options{Addr: addr, Token: "tok", ID: string(info.ID), Label: info.Name, ViewerColors: &terminal.TerminalColors{Foreground: "#d8dee9", Background: "#282c34"}}, u.slave, u.slave, winch)
		done <- result{r.Detached, e}
	}()
	eventually(t, "pane and background", func() bool {
		u.mu.Lock()
		defer u.mu.Unlock()
		return strings.Contains(u.screen.Line(23), "attached") && strings.Contains(u.screen.Line(23), "jk: back") && u.screen.Cell(50, 0).Style.Bg == phosphor.RGBColor(48, 52, 60)
	})
	eventually(t, "reserved row", func() bool { s, _ := mgr.Screen(info.ID, false); return s.Rows == 23 })
	u.resize(t, 100, 30)
	winch <- os.Interrupt
	eventually(t, "reserved row after resize", func() bool { s, _ := mgr.Screen(info.ID, false); return s.Cols == 100 && s.Rows == 29 })
	eventually(t, "visible footer after resize", func() bool {
		u.mu.Lock()
		defer u.mu.Unlock()
		return strings.Contains(u.screen.Line(29), "jk: back")
	})
	u.typeKeys(t, "jk")
	select {
	case r := <-done:
		if !r.detached || r.err != nil {
			t.Fatal(r)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("did not detach")
	}
	if current, _ := mgr.Get(info.ID); current.Status != terminal.StatusRunning {
		t.Fatal("detach stopped worker")
	}
}
func TestInstalledCodexPaneComposer(t *testing.T) {
	binary := os.Getenv("PTYD_CODEX_TEST_BIN")
	if binary == "" {
		t.Skip("set PTYD_CODEX_TEST_BIN for installed Codex pane witness")
	}
	binary, err := exec.LookPath(binary)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	profile := filepath.Join(root, "profile")
	workspace := filepath.Join(root, "workspace")
	for _, path := range []string{profile, workspace} {
		if err := os.Mkdir(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	config := "check_for_update_on_startup=false\nmodel=\"ptyd-local-fixture\"\nmodel_provider=\"fixture\"\n[model_providers.fixture]\nname=\"Local fixture\"\nbase_url=\"http://127.0.0.1:1\"\nwire_api=\"responses\"\nrequires_openai_auth=false\n[projects." + strconv.Quote(workspace) + "]\ntrust_level=\"trusted\"\n"
	if err := os.WriteFile(filepath.Join(profile, "config.toml"), []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(key, "CODEX_") || strings.HasPrefix(key, "OPENAI_") || strings.HasPrefix(key, "CHATGPT_") {
			t.Setenv(key, "")
		}
	}
	t.Setenv("CODEX_HOME", profile)
	t.Setenv("NO_COLOR", "1")
	t.Setenv("TERM", "dumb")
	mgr, addr := startDaemon(t)
	info, err := mgr.Create(terminal.CreateConfig{Name: "Codex composer", Command: binary, Args: []string{"--no-daemon"}, CWD: workspace, Env: map[string]string{"TERM": "xterm-256color", "COLORTERM": "truecolor"}, TerminalColors: &terminal.TerminalColors{Foreground: "#d8dee9", Background: "#282c34"}})
	if err != nil {
		t.Fatal(err)
	}
	u := newUserTerminal(t, 120, 40)
	done := make(chan result, 1)
	go func() {
		r, e := Attach(context.Background(), Options{Addr: addr, Token: "tok", ID: string(info.ID), Label: info.Name}, u.slave, u.slave, nil)
		done <- result{r.Detached, e}
	}()
	blank := 0
	eventually(t, "installed Codex composer background and ptyd footer", func() bool {
		u.mu.Lock()
		defer u.mu.Unlock()
		blank = 0
		for y := 0; y < 39; y++ {
			for x := 0; x < 120; x++ {
				c := u.screen.Cell(x, y)
				if (c.Rune == ' ' || c.Rune == 0) && c.Style.Bg != phosphor.DefaultColor {
					blank++
				}
			}
		}
		return blank >= 120 && strings.Contains(u.screen.Line(39), "Codex composer") && strings.Contains(u.screen.Line(39), "jk: back")
	})
	t.Logf("installed Codex pane retained %d blank background cells above a persistent status row", blank)
	u.typeKeys(t, "jk")
	select {
	case r := <-done:
		if !r.detached || r.err != nil {
			t.Fatal(r)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("pane did not detach")
	}
	if row, _ := mgr.Get(info.ID); row.Status != terminal.StatusRunning {
		t.Fatal("pane stopped Codex")
	}
}

func TestInstalledClaudePaneComposer(t *testing.T) {
	binary := os.Getenv("PTYD_CLAUDE_TEST_BIN")
	if binary == "" {
		t.Skip("set PTYD_CLAUDE_TEST_BIN for installed Claude pane witness")
	}
	binary, err := filepath.Abs(binary)
	if err != nil {
		t.Fatal(err)
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	profile, workspace := filepath.Join(root, "profile"), filepath.Join(root, "workspace")
	for _, path := range []string{profile, workspace} {
		if err := os.Mkdir(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(key, "CLAUDE_") || strings.HasPrefix(key, "ANTHROPIC_") {
			t.Setenv(key, "")
		}
	}
	t.Setenv("CLAUDE_CONFIG_DIR", profile)
	t.Setenv("ANTHROPIC_API_KEY", "ptyd-local-fixture-key")
	t.Setenv("ANTHROPIC_BASE_URL", "http://127.0.0.1:1")
	t.Setenv("CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC", "1")
	t.Setenv("DISABLE_AUTOUPDATER", "1")
	config := map[string]any{"hasCompletedOnboarding": true, "theme": "dark", "projects": map[string]any{workspace: map[string]any{"hasTrustDialogAccepted": true}}, "customApiKeyResponses": map[string]any{"approved": []string{"ptyd-local-fixture-key", "ptyd-local-fixture-key"[2:]}, "rejected": []any{}}}
	raw, _ := json.Marshal(config)
	if err := os.WriteFile(filepath.Join(profile, ".claude.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	fixtureEnv := os.Environ()
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		stop := exec.CommandContext(ctx, binary, "daemon", "stop", "--any")
		stop.Env = fixtureEnv
		stop.Dir = workspace
		stop.CombinedOutput()
	}()
	mgr, addr := startDaemon(t)
	info, err := mgr.Create(terminal.CreateConfig{Name: "Claude Code", Command: binary, Args: []string{"--model", "haiku", "--permission-mode", "plan"}, CWD: workspace, Env: map[string]string{"TERM": "xterm-256color", "COLORTERM": "truecolor"}, TerminalColors: &terminal.TerminalColors{Foreground: "#d8dee9", Background: "#282c34"}})
	if err != nil {
		t.Fatal(err)
	}
	u := newUserTerminal(t, 100, 30)
	winch := make(chan os.Signal, 1)
	done := make(chan result, 1)
	go func() {
		r, e := Attach(context.Background(), Options{Addr: addr, Token: "tok", ID: string(info.ID), Label: info.Name}, u.slave, u.slave, winch)
		done <- result{r.Detached, e}
	}()
	eventually(t, "Claude banner and attached footer", func() bool {
		u.mu.Lock()
		defer u.mu.Unlock()
		return strings.Contains(strings.Join(u.screen.Lines(), "\n"), "Claude Code") && strings.Contains(u.screen.Line(29), "jk: back")
	})
	draft := "PTYD_CLAUDE_DRAFT 雪"
	u.typeKeys(t, draft)
	eventually(t, "Claude composer accepts Unicode draft", func() bool { return strings.Contains(u.text(), draft) })
	u.resize(t, 80, 24)
	winch <- os.Interrupt
	eventually(t, "Claude layout after resize", func() bool {
		app, e := mgr.Screen(info.ID, false)
		if e != nil || app.Rows != 23 || app.Cols != 80 {
			return false
		}
		u.mu.Lock()
		defer u.mu.Unlock()
		if !strings.Contains(u.screen.Line(23), "Claude Code") || !strings.Contains(u.screen.Line(23), "jk: back") {
			return false
		}
		for y := 0; y < 23; y++ {
			if u.screen.Line(y) != app.Lines[y] {
				return false
			}
		}
		return u.screen.Cursor().X == app.CursorX && u.screen.Cursor().Y == app.CursorY
	})
	u.typeKeys(t, "jk")
	select {
	case r := <-done:
		if !r.detached || r.err != nil {
			t.Fatal(r)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Claude pane did not detach")
	}
	if row, _ := mgr.Get(info.ID); row.Status != terminal.StatusRunning {
		t.Fatal("detach stopped Claude")
	}
	t.Log("installed Claude pane: banner, Unicode draft, resized screen/cursor, footer and detach survival passed")
}

func TestPanePaletteAndNarrowStatus(t *testing.T) {
	for _, width := range []int{25, 30, 34, 35, 40, 80} {
		var wire bytes.Buffer
		p := newPane(&wire, "Codex", width, 10)
		host := phosphor.New(width, 10)
		p.Write([]byte("\x1b]4;1;rgb:12/34/56\a\x1b]10;rgb:ab/cd/ef\a\x1b]11;rgb:28/2c/34\a\x1b[31mX\x1b[0mY"))
		p.draw(false)
		host.Write(wire.Bytes())
		if !strings.Contains(host.Line(9), "jk: back") {
			t.Fatalf("hint clipped at%d: %q", width, host.Line(9))
		}
		pal := host.Palette()
		if got := pal.Resolve(host.Cell(0, 0).Style.Fg, false); got != (phosphor.RGB{R: 18, G: 52, B: 86}) {
			t.Fatalf("indexed palette not preserved: %+v", got)
		}
		if got := pal.Resolve(host.Cell(1, 0).Style.Fg, false); got != (phosphor.RGB{R: 171, G: 205, B: 239}) {
			t.Fatalf("foreground palette not preserved: %+v", got)
		}
		if got := pal.Resolve(host.Cell(40%width, 0).Style.Bg, true); got != (phosphor.RGB{R: 40, G: 44, B: 52}) {
			t.Fatalf("background palette not preserved: %+v", got)
		}
	}
}
func TestPaneRetainedHistoryIsAvailableInHostScrollback(t *testing.T) {
	var wire bytes.Buffer
	p := newPane(&wire, "logs", 40, 8)
	host := phosphor.New(40, 8, phosphor.WithScrollback(100))
	for i := 0; i < 30; i++ {
		p.Write([]byte(fmt.Sprintf("line-%02d\r\n", i)))
		p.draw(false)
		host.Write(wire.Bytes())
		wire.Reset()
	}
	history := strings.Join(host.ScrollbackLines(), "\n")
	for i := 0; i < 20; i++ {
		if !strings.Contains(history, fmt.Sprintf("line-%02d", i)) {
			t.Fatalf("missing line%d in host scrollback: %s", i, history)
		}
	}
	if !strings.Contains(host.Line(7), "jk: back") {
		t.Fatal("scrolling erased footer")
	}
}

func TestQueuedHistoryReflowsWhenResizeOvertakesDraw(t *testing.T) {
	var wire bytes.Buffer
	p := newPane(&wire, "logs", 8, 3)
	host := phosphor.New(8, 3, phosphor.WithScrollback(100))
	p.draw(false)
	host.Write(wire.Bytes())
	wire.Reset()
	p.Write([]byte("ABCDEFGH\r\nIJKLMNOP\r\n"))
	p.resize(4, 3)
	host.Resize(4, 3)
	p.draw(false)
	host.Write(wire.Bytes())
	all := strings.Join(host.ScrollbackLines(), "\n") + "\n" + strings.Join(host.Lines(), "\n")
	if !strings.Contains(all, "EFGH") {
		t.Fatalf("old-width suffix lost: %s", all)
	}
}

func TestFinishedPaneFlushesUnfinishedSynchronizedUpdate(t *testing.T) {
	var out bytes.Buffer
	p := newPane(&out, "fixture", 80, 24)
	p.Write([]byte("READY"))
	if err := p.draw(false); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	p.Write([]byte("\x1b[?2026h\x1b[HSTARTUP_FAILED"))
	if err := p.draw(false); err != nil {
		t.Fatal(err)
	}
	if out.Len() != 0 {
		t.Fatal("synchronized update painted early")
	}
	p.finish()
	if err := p.draw(false); err != nil {
		t.Fatal(err)
	}
	screen := phosphor.New(80, 24)
	screen.Write(out.Bytes())
	if !strings.Contains(screen.Line(0), "STARTUP_FAILED") || !strings.Contains(screen.Line(23), "finished") {
		t.Fatalf("final error hidden: %q / %q", screen.Line(0), screen.Line(23))
	}
}

func TestPaneScrollDamagePreservesRenderedState(t *testing.T) {
	for _, width := range []int{40, 120} {
		t.Run(strconv.Itoa(width), func(t *testing.T) {
			var wire bytes.Buffer
			p := newPane(&wire, "group / transcript", width, 24)
			host := phosphor.New(width, 24, phosphor.WithScrollback(200))
			check := func() {
				t.Helper()
				if err := p.draw(false); err != nil {
					t.Fatal(err)
				}
				host.Write(wire.Bytes())
				app := p.screen
				cols, rows := app.Size()
				for y := 0; y < rows; y++ {
					for x := 0; x < cols; x++ {
						want, got := app.Cell(x, y), host.Cell(x, y)
						for _, c := range []*phosphor.Cell{&want, &got} {
							if c.Rune == 0 && c.Width != 0 {
								c.Rune = ' '
							}
						}
						// Program palette changes are emitted as RGB, so compare the
						// displayed colors as well as attributes and cluster structure.
						for _, pair := range []struct {
							cell    *phosphor.Cell
							palette phosphor.Palette
							reverse bool
						}{{&want, app.Palette(), app.Modes().ReverseVideo}, {&got, host.Palette(), host.Modes().ReverseVideo}} {
							fg, bg := pair.palette.Resolve(pair.cell.Style.Fg, false), pair.palette.Resolve(pair.cell.Style.Bg, true)
							if (pair.cell.Style.Attrs&phosphor.AttrInverse != 0) != pair.reverse {
								fg, bg = bg, fg
							}
							pair.cell.Style.Attrs &^= phosphor.AttrInverse
							pair.cell.Style.Fg = phosphor.RGBColor(fg.R, fg.G, fg.B)
							pair.cell.Style.Bg = phosphor.RGBColor(bg.R, bg.G, bg.B)
						}
						if want != got {
							t.Fatalf("cell %d,%d: got %+v want %+v", x, y, got, want)
						}
					}
				}
				if want, got := app.Cursor(), host.Cursor(); want.X != got.X || want.Y != got.Y || want.Visible != got.Visible {
					t.Fatalf("cursor got %+v want %+v", got, want)
				}
				if !strings.Contains(host.Line(rows), "jk: back") {
					t.Fatal("scroll damaged the footer")
				}
			}
			paint := func(offset int) {
				var b strings.Builder
				b.WriteString("\x1b[?2026h")
				for y := 0; y < 23; y++ {
					id := y + offset
					if y < 2 || y >= 20 {
						id = y
					}
					fmt.Fprintf(&b, "\x1b[%d;1H\x1b[0m\x1b[48;2;%d;30;40m\x1b[2K\x1b[4:3;31m", y+1, 20+id%20)
					if id%5 != 0 {
						fmt.Fprintf(&b, "\x1b]8;id=row%d;https://example.com/%d\x1b\\line %03d \x1b[?2027h雪 👩‍💻 é\x1b[?2027l\x1b]8;;\x1b\\", id, id, id)
					}
				}
				b.WriteString("\x1b[0m\x1b[22;5H\x1b[?2026l")
				p.Write([]byte(b.String()))
			}
			up, down := false, false
			for _, offset := range []int{0, 1, 2, 5, 21, 5, 2, 1, 0, 1, 2} {
				paint(offset)
				check()
				up = up || strings.Contains(wire.String(), "S\x1b[r")
				down = down || strings.Contains(wire.String(), "T\x1b[r")
				wire.Reset()
			}
			if !up || !down {
				t.Fatal("fixture did not exercise scroll moves in both directions")
			}
			paint(2)
			check()
			if strings.Contains(wire.String(), "line ") || strings.Contains(wire.String(), "jk: back") {
				t.Fatal("identical redraw retransmitted unchanged rows or footer")
			}
			wire.Reset()
			saved := p.screen.Snapshot()
			for _, write := range []string{"\x1b]4;1;#aabbcc\x1b\\", "\x1b[?1049h\x1b[2Jalternate", "\x1b[?1049l", "\x1b[?5h", "\x1b[?5l"} {
				p.Write([]byte(write))
				check()
				wire.Reset()
			}
			if err := p.restore(saved); err != nil {
				t.Fatal(err)
			}
			check()
			wire.Reset()
			p.resize(width+4, 26)
			p.screen.Resize(width+4, 25)
			host.Resize(width+4, 26)
			check()
		})
	}
}

func TestPaneStatusThemeWidthAndCaching(t *testing.T) {
	for _, theme := range []struct{ name, fg, bg string }{{"dark", "#d8dee9", "#282c34"}, {"light", "#20252b", "#fafafa"}} {
		for _, state := range []string{"attached", "viewing", "finished"} {
			for width := 1; width <= 100; width++ {
				var wire bytes.Buffer
				p := newPane(&wire, "project / codex", width, 8)
				p.viewOnly = state == "viewing"
				p.finished = state == "finished"
				p.Write([]byte("\x1b]10;" + theme.fg + "\a\x1b]11;" + theme.bg + "\aAPP"))
				if err := p.draw(false); err != nil {
					t.Fatal(err)
				}
				host := phosphor.New(width, 8)
				host.Write(wire.Bytes())
				line := host.Line(7)
				if width >= 8 && !strings.Contains(line, "jk: back") {
					t.Fatalf("%s/%s/%d lost back hint: %q", theme.name, state, width, line)
				}
				if width >= 48 && !strings.Contains(line, state) {
					t.Fatalf("state missing: %q", line)
				}
				for x := 0; x < width; x++ {
					if host.Cell(x, 7).Style.Attrs&phosphor.AttrInverse != 0 {
						t.Fatal("status used inverse video")
					}
					if host.Cell(x, 7).Style.Bg != host.Cell(0, 7).Style.Bg {
						t.Fatal("status background has a gap")
					}
				}
				wire.Reset()
				p.Write([]byte("\x1b[HNEW"))
				p.draw(false)
				if strings.Contains(wire.String(), "jk: back") {
					t.Fatal("app output repainted the unchanged status bar")
				}
				if width == 80 {
					wire.Reset()
					p.Write([]byte("\x1b]11;#567890\a"))
					p.draw(false)
					if !strings.Contains(wire.String(), "jk: back") {
						t.Fatal("changed background did not refresh status")
					}
				}
			}
		}
	}
}

func TestPaneStatusUnicodeKeepsEdgesAcrossPaletteChanges(t *testing.T) {
	var wire bytes.Buffer
	p := newPane(&wire, "repo / 👩‍💻 work", 80, 8)
	host := phosphor.New(80, 8)
	for _, write := range []string{"\x1b[?2027h\x1b[48;2;30;40;50m\x1b[2J", "\x1b]10;#aabbcc\a", "\x1b]11;#223344\a", "\x1b[Hupdate"} {
		p.Write([]byte(write))
		if err := p.draw(false); err != nil {
			t.Fatal(err)
		}
		host.Write(wire.Bytes())
		wire.Reset()
		if line := host.Line(7); strings.Count(line, "jk: back") != 1 || !strings.HasSuffix(line, "jk: back") {
			t.Fatalf("status shifted: %q", line)
		}
		for x := 0; x < 80; x++ {
			if host.Cell(x, 7).Style.Bg != host.Cell(0, 7).Style.Bg {
				t.Fatalf("background gap at %d", x)
			}
		}
		if !host.Modes().GraphemeClustering {
			t.Fatal("status did not restore app join mode")
		}
	}
}
