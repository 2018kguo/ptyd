package main

import (
	"fmt"
	"github.com/2018kguo/phosphor"
	"github.com/2018kguo/ptyd/internal/terminal"
	"strings"
	"testing"
)

func TestWorkspacePromptsStayInsideTheirFrame(t *testing.T) {
	for _, size := range [][2]int{{40, 16}, {80, 24}, {120, 30}, {200, 60}} {
		cols, rows := size[0], size[1]
		for _, mode := range []workspaceMode{wsRename, wsRun, wsNewGroup, wsConfirm, wsResume, wsMove, wsLaunch, wsHelp} {
			v := workspaceView{color: true, text: strings.Repeat("very-long-雪-name-", 30), groups: []terminal.GroupSummary{{Group: terminal.Group{ID: "work", Name: strings.Repeat("long-name", 20)}}}}
			base := phosphor.New(cols, rows)
			base.Write([]byte(workspaceFrame(v, cols, rows)))
			v.mode = mode
			host := phosphor.New(cols, rows)
			host.Write([]byte(workspaceFrame(v, cols, rows)))
			width := min(cols-4, 70)
			x, y := max((cols-width)/2, 1), max((rows-14)/2, 2)
			for sy := 0; sy < rows; sy++ {
				for sx := 0; sx < cols; sx++ {
					// The action bar follows the open dialog's controls.
					if sy == rows-2 && sx >= 2 && sx < cols-2 {
						continue
					}
					if sx >= x && sx < x+width && sy >= y && sy < y+12 {
						continue
					}
					if host.Cell(sx, sy) != base.Cell(sx, sy) {
						t.Fatalf("mode %d on %dx%d escaped dialog at %d,%d", mode, cols, rows, sx, sy)
					}
				}
			}
			if mode != wsHelp && strings.Contains(host.Line(rows-2), "q quit") {
				t.Fatal("dialog advertised a quit key that edits its field")
			}
			if mode == wsRename || mode == wsRun || mode == wsNewGroup || mode == wsConfirm || mode == wsResume {
				c := host.Cursor()
				if c.X <= x || c.X >= x+width-1 || c.Y != y+7 {
					t.Fatalf("input caret outside dialog: %+v", c)
				}
				if !strings.Contains(host.Line(y+7), "雪-name-") {
					t.Fatal("long input did not keep its end visible")
				}
			}
		}
	}
}

func TestWorkspaceLongListsKeepTheirBottomBorders(t *testing.T) {
	for _, size := range [][2]int{{120, 29}, {120, 30}, {200, 60}} {
		cols, rows := size[0], size[1]
		for _, group := range []terminal.GroupID{"", "work"} {
			v := workspaceView{group: group}
			for i := 0; i < 80; i++ {
				id := fmt.Sprintf("item-%d", i)
				v.groups = append(v.groups, terminal.GroupSummary{Group: terminal.Group{ID: terminal.GroupID(id), Name: id}, Running: 1, Terminals: 1})
				v.rows = append(v.rows, terminal.SessionSummary{ID: terminal.ID(id), Name: id, State: terminal.OverviewRunning})
				v.selected = id
			}
			host := phosphor.New(cols, rows)
			host.Write([]byte(workspaceFrame(v, cols, rows)))
			left := min(max(cols/3, 30), 42)
			if group != "" {
				left = min(max(cols/3, 30), 44)
			}
			for x := 1; x <= left; x++ {
				want := '─'
				if x == 1 {
					want = '╰'
				}
				if x == left {
					want = '╯'
				}
				if got := host.Cell(x, rows-6).Rune; got != want {
					t.Fatalf("group=%q %dx%d list painted over border at %d: %c", group, cols, rows, x, got)
				}
			}
		}
	}
}
func TestWorkspaceFooterHasBreathingRoom(t *testing.T) {
	for _, cols := range []int{40, 60, 80, 120, 200} {
		for _, group := range []terminal.GroupID{"", "work"} {
			host := phosphor.New(cols, 24)
			host.Write([]byte(workspaceFrame(workspaceView{group: group}, cols, 24)))
			if strings.TrimSpace(host.Line(23)) != "" {
				t.Fatal("controls occupy the terminal's clipped bottom edge")
			}
			bar := host.Line(22)
			for _, hint := range []string{"Enter open", "jk back", "? help", "q quit"} {
				if !strings.Contains(bar, hint) {
					t.Fatalf("%d columns lost %q: %s", cols, hint, bar)
				}
			}
		}
	}
}
func TestWorkspaceThemeFollowsLightAndDarkTerminals(t *testing.T) {
	for _, colors := range []*terminal.TerminalColors{{Foreground: "#e0e0e0", Background: "#202020"}, {Foreground: "#202020", Background: "#e0e0e0"}} {
		theme := workspaceThemeFor(colors)
		host := phosphor.New(40, 16)
		host.Write([]byte("\x1b[" + theme.selected() + "m selected "))
		c := host.Cell(0, 0)
		if c.Style.Attrs&phosphor.AttrInverse != 0 || c.Style.Bg.Kind() != phosphor.ColorRGB {
			t.Fatal("selection did not use a soft terminal-relative surface")
		}
		rgb := c.Style.Bg.RGB()
		if colors.Background == "#202020" && rgb.R >= 100 || colors.Background == "#e0e0e0" && rgb.R <= 160 {
			t.Fatalf("surface lost the terminal's tone: %+v", rgb)
		}
	}
}

func TestWorkspaceInputTailKeepsWideTextSuffix(t *testing.T) {
	text := strings.Repeat("雪", 2050) + "-REAL-END"
	if got := workspaceInputTail(text, 29); !strings.HasSuffix(got, "-REAL-END") || textWidth(got) > 29 {
		t.Fatalf("wide input lost its actual end: %q", got)
	}
}

func TestWorkspaceNarrowPaneCountStaysVisible(t *testing.T) {
	v := workspaceView{group: "work", selected: "four", pins: map[terminal.GroupID][]terminal.ID{"work": {"one", "two", "three"}}, snapshot: &workspaceSnapshot{Query: workspaceQuery{Group: "work"}, PreviewID: "four", Preview: phosphor.New(80, 24)}}
	for _, id := range []terminal.ID{"one", "two", "three", "four"} {
		v.rows = append(v.rows, terminal.SessionSummary{ID: id, Name: string(id), State: terminal.OverviewRunning})
	}
	for _, size := range [][2]int{{40, 16}, {80, 24}} {
		host := phosphor.New(size[0], size[1])
		host.Write([]byte(workspaceFrame(v, size[0], size[1])))
		if !strings.Contains(host.Line(4), "1/4 panes") {
			t.Fatalf("narrow view hid pane visibility: %s", host.Line(4))
		}
		if !strings.Contains(strings.Join(host.Lines(), "\n"), "four") {
			t.Fatal("narrow layout omitted selected terminal")
		}
	}
	v.rows = v.rows[:2]
	v.selected = "two"
	v.pins["work"] = []terminal.ID{"one", "two"}
	host := phosphor.New(80, 24)
	host.Write([]byte(workspaceFrame(v, 80, 24)))
	if !strings.Contains(host.Line(4), "2/2 panes") {
		t.Fatalf("short list wasted space needed for live panes: %s", host.Line(4))
	}
}
