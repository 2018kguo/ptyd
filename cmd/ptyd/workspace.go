package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"slices"
	"strings"
	"syscall"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/2018kguo/ptyd/internal/client"
	"github.com/2018kguo/ptyd/internal/native"
	"github.com/2018kguo/ptyd/internal/terminal"
	"golang.org/x/term"
)

type workspaceMode uint8

const (
	wsNormal workspaceMode = iota
	wsSearch
	wsCommand
	wsNewGroup
	wsLaunch
	wsRun
	wsRename
	wsMove
	wsConfirm
	wsResume
	wsHelp
)

type workspaceQuery struct {
	Generation uint64
	Group      terminal.GroupID
	Selected   string
	Focus      terminal.ID
	Search     string
	History    bool
	Outside    bool
	Watches    []terminal.ID
}
type workspaceSnapshot struct {
	Query    workspaceQuery
	Groups   []terminal.GroupSummary
	Overview terminal.Overview
	Panes    []workspacePane
	Titles   map[terminal.ID]string
	Search   *client.ContentSearch
	Matches  map[terminal.ID]*client.ContentMatch
	Err      error
}
type workspaceView struct {
	generation              uint64 // attachment handoffs fence input and stale refreshes
	group                   terminal.GroupID
	selected                string
	previewFocus            terminal.ID
	groups                  []terminal.GroupSummary
	rows                    []terminal.SessionSummary
	snapshot                *workspaceSnapshot
	mode                    workspaceMode
	text, query, notice     string
	history, outside, color bool
	err                     error
	target                  string
	aux                     int
	busy                    bool
	home                    workspaceHomeState
	pins                    map[terminal.GroupID][]terminal.ID
	captains                map[terminal.GroupID]terminal.ID
	titles                  map[terminal.ID]string
	searchBefore            string
	motion                  bool
	brandPhase              int
	theme                   workspaceTheme
	agentAccess             workspaceAgentAccess
}

func (v workspaceView) groupInfo() (terminal.GroupSummary, bool) {
	for _, g := range v.groups {
		if g.ID == v.group {
			return g, true
		}
	}
	return terminal.GroupSummary{}, false
}
func (v workspaceView) visibleGroups() []terminal.GroupSummary {
	var out []terminal.GroupSummary
	for _, g := range v.groups {
		if !v.history && (g.Archived || (g.ID == terminal.DefaultGroup && g.Running == 0)) {
			continue
		}
		out = append(out, g)
	}
	return out
}
func (v workspaceView) items() []string {
	var out []string
	if v.group == "" {
		for _, g := range v.visibleGroups() {
			out = append(out, string(g.ID))
		}
	} else {
		for _, r := range v.rows {
			out = append(out, string(r.ID))
		}
	}
	return out
}
func (v workspaceView) index() int {
	for i, id := range v.items() {
		if id == v.selected {
			return i
		}
	}
	return 0
}
func (v *workspaceView) move(n int) {
	if v.group == "" {
		v.previewFocus = ""
	}
	ids := v.items()
	if len(ids) > 0 {
		v.selected = ids[min(max(v.index()+n, 0), len(ids)-1)]
	}
}
func (v workspaceView) row() (terminal.SessionSummary, bool) {
	for _, r := range v.rows {
		if string(r.ID) == v.selected {
			return r, true
		}
	}
	return terminal.SessionSummary{}, false
}
func (v *workspaceView) refresh(s workspaceSnapshot) {
	if !s.Query.matches(*v) {
		return
	}
	if s.Err != nil {
		v.err = s.Err
		return
	}
	v.err = nil
	v.groups = s.Groups
	v.snapshot = &s
	if v.titles == nil {
		v.titles = map[terminal.ID]string{}
	}
	for id, title := range s.Titles {
		v.titles[id] = title
	}
	for _, p := range s.Panes {
		if p.Removed {
			v.unpin(p.Row.ID)
		}
	}
	if v.group != "" {
		v.rows = s.Overview.Sessions
	}
	ids := v.items()
	for _, id := range ids {
		if id == v.selected {
			return
		}
	}
	v.selected = ""
	if len(ids) > 0 {
		v.selected = ids[0]
	}
}
func (q workspaceQuery) matches(v workspaceView) bool {
	return q.Generation == v.generation && q.Group == v.group && q.Search == v.query && q.History == v.history && q.Outside == v.outside && slices.Equal(q.Watches, v.watched())
}
func queueWorkspace(ch chan workspaceQuery, v workspaceView) {
	q := workspaceQuery{Generation: v.generation, Group: v.group, Selected: v.selected, Focus: v.previewFocus, Search: v.query, History: v.history, Outside: v.outside, Watches: append([]terminal.ID(nil), v.watched()...)}
	select {
	case <-ch:
	default:
	}
	ch <- q
}

type workspaceKey struct {
	Rune      rune
	Direction int
	Escape    bool
	Paste     bool
	Back      bool
	Mouse     *client.MouseEvent
}
type workspaceInput struct {
	pending []byte
	paste   bool
	since   time.Time
	lastJ   time.Time
}

func (p *workspaceInput) feed(chunk []byte, now time.Time) []workspaceKey {
	p.pending = append(p.pending, chunk...)
	var out []workspaceKey
	if len(p.pending) > 65536 {
		p.pending = nil
		p.paste = false
		p.lastJ = time.Time{}
		return out
	}
	for len(p.pending) > 0 {
		if p.paste {
			end := []byte("\x1b[201~")
			if bytes.HasPrefix(p.pending, end) {
				p.pending = p.pending[len(end):]
				p.paste = false
				continue
			}
			if bytes.HasPrefix(end, p.pending) {
				break
			}
			if !utf8.FullRune(p.pending) {
				break
			}
			r, n := utf8.DecodeRune(p.pending)
			p.pending = p.pending[n:]
			if r == '\r' || r == '\n' || r == '\t' {
				r = ' '
			}
			if !unicode.IsControl(r) {
				out = append(out, workspaceKey{Rune: r, Paste: true})
			}
			continue
		}
		if p.pending[0] == 0x1b {
			p.lastJ = time.Time{}
			if len(p.pending) == 1 {
				if p.since.IsZero() {
					p.since = now
				}
				break
			}
			n, d := statusEscape(p.pending)
			if n == 0 {
				break
			}
			mouse, isMouse := client.ParseMouseReport(p.pending[:n])
			paste := bytes.Equal(p.pending[:n], []byte("\x1b[200~"))
			p.pending = p.pending[n:]
			p.since = time.Time{}
			if isMouse {
				out = append(out, workspaceKey{Mouse: &mouse})
			} else if paste {
				p.paste = true
			} else if d != 0 {
				out = append(out, workspaceKey{Direction: d})
			} else if n == 1 {
				out = append(out, workspaceKey{Escape: true})
			}
			continue
		}
		if !utf8.FullRune(p.pending) {
			break
		}
		r, n := utf8.DecodeRune(p.pending)
		p.pending = p.pending[n:]
		if r == 'k' && !p.lastJ.IsZero() && now.Sub(p.lastJ) < client.BackKeyWindow {
			out = append(out, workspaceKey{Back: true})
			p.lastJ = time.Time{}
		} else {
			p.lastJ = time.Time{}
			if r == 'j' {
				p.lastJ = now
			}
			out = append(out, workspaceKey{Rune: r})
		}
	}
	return out
}
func (p *workspaceInput) expire(now time.Time) []workspaceKey {
	if len(p.pending) == 1 && p.pending[0] == 0x1b && !p.since.IsZero() && now.Sub(p.since) >= 100*time.Millisecond {
		p.pending = nil
		p.since = time.Time{}
		return []workspaceKey{{Escape: true}}
	}
	return nil
}

type workspaceOperation struct {
	Epoch    uint64
	Group    *terminal.Group
	Terminal *terminal.Info
	Notice   string
	Err      error
}

func workspace(c client.API, start terminal.GroupID, initial terminal.ID, in, out *os.File, showHistory ...bool) error {
	if !term.IsTerminal(int(in.Fd())) || !term.IsTerminal(int(out.Fd())) {
		return fmt.Errorf("attach needs an interactive terminal; use ptyd ls --json or ptyd status --json")
	}
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGHUP)
	defer cancel()
	raw, err := term.MakeRaw(int(in.Fd()))
	if err != nil {
		return err
	}
	defer func() {
		if raw != nil {
			_ = term.Restore(int(in.Fd()), raw)
		}
		_, _ = io.WriteString(out, "\x18"+"\x1b[?1000l\x1b[?1006l\x1b[?2026l\x1b[?2004l\x1b[?7h\x1b[0m\x1b[?25h\x1b[?1049l\x1b[23;0t")
	}()
	colors, pending := client.ProbeColors(ctx, in, out)
	viewerKeyboard, keyboardPending := client.ProbeKeyboard(ctx, in, out)
	pending = append(pending, keyboardPending...)
	input, err := client.NewInput(ctx, in, pending)
	if err != nil {
		return err
	}
	defer func() { input.Close() }()
	_, _ = io.WriteString(out, "\x1b[22;0t"+statusEnter)
	history := start != "" || (len(showHistory) > 0 && showHistory[0])
	layouts := loadWorkspaceLayouts(c.Addr)
	v := workspaceView{theme: workspaceThemeFor(colors), pins: layouts.pins, motion: os.Getenv("PTYD_REDUCED_MOTION") == "", notice: layouts.notice, history: history, group: start, selected: string(initial), color: os.Getenv("NO_COLOR") == "" && os.Getenv("TERM") != "dumb"}
	v.captains = layouts.captains
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/sh"
	}
	queries := make(chan workspaceQuery, 1)
	updates := make(chan workspaceSnapshot, 1)
	var stopRefresh context.CancelFunc
	var refreshDone chan struct{}
	startRefresh := func() {
		var refreshCtx context.Context
		refreshCtx, stopRefresh = context.WithCancel(ctx)
		refreshDone = make(chan struct{})
		go func() { defer close(refreshDone); refreshWorkspace(refreshCtx, c, queries, updates) }()
	}
	startRefresh()
	defer func() { stopRefresh(); <-refreshDone }()
	queueWorkspace(queries, v)
	resize := make(chan os.Signal, 1)
	signal.Notify(resize, syscall.SIGWINCH)
	defer signal.Stop(resize)
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	var parser workspaceInput
	ops := make(chan workspaceOperation, 1)
	var operationCancel context.CancelFunc
	var operationEpoch uint64
	defer func() {
		if operationCancel != nil {
			operationCancel()
		}
	}()
	var lastFrame string
	var frameCols, frameRows int
	var sceneHits []workspaceHit
	var mouse workspaceMouse
	nextMotion := time.Now().Add(workspaceMotionInterval)
	draw := func() {
		cols, rows, e := term.GetSize(int(out.Fd()))
		if e != nil {
			cols, rows = 80, 24
		}
		frameCols, frameRows = cols, rows
		stable := v
		stable.brandPhase = 0
		scene := renderWorkspace(stable, cols, rows)
		frame := scene.ANSI
		sceneHits = scene.Hits
		if frame != lastFrame {
			if v.brandPhase != 0 {
				// Reuse the painted panes; only the small header animation differs.
				brand := workspaceBrand(cols, rows, 0, v.color)
				_, _ = io.WriteString(out, strings.TrimSuffix(frame, brand)+workspaceBrand(cols, rows, v.brandPhase, v.color))
			} else {
				_, _ = io.WriteString(out, frame)
			}
			lastFrame = frame
		}
	}
	launch := func(fn func(context.Context) workspaceOperation) {
		if v.busy {
			return
		}
		operationEpoch++
		epoch := operationEpoch
		v.busy = true
		v.notice = "Working… jk goes back. Started work stays in its group."
		opCtx, stop := context.WithTimeout(ctx, 30*time.Second)
		operationCancel = stop
		go func() {
			r := fn(opCtx)
			r.Epoch = epoch
			select {
			case ops <- r:
			case <-ctx.Done():
			}
		}()
	}
	createTerminal := func(cfg terminal.CreateConfig) {
		g, ok := v.groupInfo()
		if !ok {
			return
		}
		cfg.GroupID, cfg.CWD, cfg.TerminalColors = g.ID, g.CWD, colors
		count := 1
		for _, r := range v.rows {
			if r.Name == cfg.Name || strings.HasPrefix(r.Name, cfg.Name+" ") {
				count++
			}
		}
		if count > 1 {
			cfg.Name = fmt.Sprintf("%s %d", cfg.Name, count)
		}
		_ = client.TerminalEnvironment().Apply(&cfg)
		launch(func(ctx context.Context) workspaceOperation {
			var t terminal.Info
			e := c.DoContext(ctx, "POST", "/terminals", cfg, &t)
			return workspaceOperation{Terminal: &t, Err: e}
		})
	}
	attachTerminal := func(info terminal.Info) error {
		v.generation++
		// The workspace is hidden while attached; pause its preview/search reads.
		stopRefresh()
		<-refreshDone
		lastFrame = ""
		input.Close()
		_ = client.FlushInput(in)
		_ = term.Restore(int(in.Fd()), raw)
		raw = nil
		_, _ = io.WriteString(out, "\x1b[?1000l\x1b[?1006l\x1b[?2004l\x1b[0m\x1b[?25h\x1b[?1049l")
		caption := client.SessionCaption{Name: info.Name, Provider: sessionProvider(terminal.SessionSummary{Agent: info.Agent, Source: info.Source, Command: info.Command}), Captain: v.captains[info.GroupID] == info.ID}
		if g, ok := v.groupInfo(); ok {
			caption.Group = g.Name
		}
		historyView := info.Status != terminal.StatusRunning
		result, attachErr := client.Attach(ctx, client.Options{
			Addr: c.Addr, Token: c.Token, ID: string(info.ID), Label: caption.Label(v.titles[info.ID]), Caption: &caption,
			Human: true, Hold: historyView, ReadOnly: historyView, ViewerColors: colors, ViewerKeyboard: &viewerKeyboard, GroupID: info.GroupID,
		}, in, out, resize)
		var unread []byte
		if result.Detached && attachErr == nil {
			unread = result.PendingInput
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		v.selected = string(info.ID)
		v.notice = "Returned to your workspace."
		if attachErr != nil {
			v.notice = "Could not open: " + attachErr.Error()
		} else if !result.Detached {
			ended, endErr := waitExited(ctx, c, info.ID)
			if ctx.Err() != nil {
				return ctx.Err()
			}
			switch {
			case endErr != nil:
				v.notice = "Connection closed: " + endErr.Error()
			case ended.Status == terminal.StatusRunning:
				v.notice = "Connection closed. The terminal is still running; Enter reconnects."
			default:
				v.showEnded(ended)
			}
		}
		raw, err = term.MakeRaw(int(in.Fd()))
		if err != nil {
			return err
		}
		if !result.Detached || attachErr != nil {
			// Input for a process that just ended must not become workspace commands.
			_ = client.FlushInput(in)
		}
		input, err = client.NewInput(ctx, in, unread)
		if err != nil {
			return err
		}
		parser = workspaceInput{}
		_, _ = io.WriteString(out, statusEnter)
		startRefresh()
		queueWorkspace(queries, v)
		return nil
	}
	importAgent := func(ref native.Ref, resume bool) {
		g := v.group
		launch(func(ctx context.Context) workspaceOperation {
			result, e := c.ImportAgent(ctx, terminal.ImportRequest{Ref: ref, GroupID: g, Resume: resume,
				TerminalEnv: client.TerminalEnvironment(), TerminalColors: colors})
			return workspaceOperation{Terminal: &result.Terminal, Err: e}
		})
	}
	autoOpen := string(initial)
	var handle func(workspaceKey) (bool, error)
	handle = func(k workspaceKey) (bool, error) {
		if k.Mouse != nil {
			event := *k.Mouse
			if cols, rows, e := term.GetSize(int(out.Fd())); e == nil && (cols != frameCols || rows != frameRows) {
				draw()
				return false, nil
			}
			if event.Button == 64 || event.Button == 65 {
				for _, h := range sceneHits {
					r := h.Rect
					if (h.Kind == workspaceHitGroup || h.Kind == workspaceHitTerminal) && event.X-1 >= r.x && event.X-1 < r.x+r.w && event.Y-1 >= r.y && event.Y-1 < r.y+r.h {
						direction := -1
						if event.Button == 65 {
							direction = 1
						}
						return handle(workspaceKey{Direction: direction})
					}
				}
				return false, nil
			}
			h, open, ok := mouse.click(event, sceneHits, time.Now())
			if !ok {
				return false, nil
			}
			if h.Kind == workspaceHitKey {
				return handle(h.Key)
			}
			if v.snapshot == nil || v.err != nil || !v.snapshot.Query.matches(v) {
				v.notice = "Wait for this view to refresh before opening it."
				return false, nil
			}
			if v.mode == wsSearch {
				v.mode = wsNormal
				v.text = ""
			}
			switch h.Kind {
			case workspaceHitGroup:
				if v.selected != h.ID {
					v.previewFocus = ""
				}
				v.selected = h.ID
				if open {
					v.enterGroup(terminal.GroupID(h.ID))
				}
			case workspaceHitPane:
				if v.group == "" {
					v.previewFocus = terminal.ID(h.ID)
					if open {
						v.enterGroup(h.Group)
						v.selected = h.ID
						autoOpen = h.ID
					}
				} else {
					v.selected = h.ID
					if open {
						return handle(workspaceKey{Rune: '\r'})
					}
				}
			case workspaceHitTerminal:
				v.selected = h.ID
				if open {
					return handle(workspaceKey{Rune: '\r'})
				}
			case workspaceHitDestination:
				index := 0
				for _, g := range v.groups {
					if g.ID == v.group || g.Archived {
						continue
					}
					if string(g.ID) == h.ID {
						v.aux = index
						if open {
							return handle(workspaceKey{Rune: '\r'})
						}
						break
					}
					index++
				}
			case workspaceHitKey:
			}
			queueWorkspace(queries, v)
			return false, nil
		}
		mouse.last = workspaceClick{}
		if k.Paste {
			switch v.mode {
			case wsSearch, wsCommand, wsNewGroup, wsRun, wsRename, wsConfirm, wsResume:
				if utf8.RuneCountInString(v.text) < 4096 {
					v.text += string(k.Rune)
				}
			}
			return false, nil
		}
		if k.Back || k.Escape {
			if v.busy {
				if operationCancel != nil {
					operationCancel()
					operationCancel = nil
				}
				operationEpoch++
				v.busy = false
				v.notice = "Stopped waiting. Started work stays in its group."
			}
			quit := v.back()
			queueWorkspace(queries, v)
			return quit, nil
		}
		if cols, rows, e := term.GetSize(int(out.Fd())); e == nil && (cols < 40 || rows < 16) {
			return k.Rune == 'q' || k.Rune == 3, nil
		}
		if v.busy {
			return k.Rune == 'q' || k.Rune == 3, nil
		}
		if k.Rune == 3 {
			return true, nil
		}
		if v.mode == wsHelp {
			if k.Rune == 'q' || k.Rune == '?' || k.Rune == '\r' {
				v.mode = wsNormal
			}
			return false, nil
		}
		if v.mode == wsLaunch {
			switch k.Rune {
			case 'f':
				if v.agentAccess == workspaceFullAccess {
					v.agentAccess = workspaceCLISettings
				} else {
					v.agentAccess = workspaceFullAccess
				}
			case 'c':
				v.mode = wsNormal
				createTerminal(v.agentAccess.launch("codex"))
			case 'a':
				v.mode = wsNormal
				createTerminal(v.agentAccess.launch("claude"))
			case 's':
				v.mode = wsNormal
				createTerminal(terminal.CreateConfig{Command: shell, Name: "shell", Restore: terminal.RestoreAuto})
			case 'r':
				v.mode = wsRun
				v.text = ""
			case 'q':
				v.mode = wsNormal
			}
			return false, nil
		}
		if v.mode == wsMove {
			available := []terminal.GroupSummary{}
			for _, g := range v.groups {
				if !g.Archived && g.ID != v.group {
					available = append(available, g)
				}
			}
			if k.Direction != 0 {
				v.aux = min(max(v.aux+k.Direction, 0), len(available)-1)
			} else {
				switch k.Rune {
				case 'j':
					v.aux = min(v.aux+1, len(available)-1)
				case 'k':
					v.aux = max(v.aux-1, 0)
				case 'q':
					v.mode = wsNormal
				case '\r', '\n':
					if len(available) > 0 {
						g := available[min(max(v.aux, 0), len(available)-1)]
						id := v.target
						v.mode = wsNormal
						launch(func(ctx context.Context) workspaceOperation {
							e := c.DoContext(ctx, "PATCH", "/terminals/"+id, terminal.TerminalUpdate{GroupID: &g.ID}, nil)
							return workspaceOperation{Notice: "Moved to " + g.Name + ".", Err: e}
						})
					}
				}
			}
			return false, nil
		}
		if v.mode != wsNormal {
			if k.Rune == 127 || k.Rune == 8 {
				if v.text != "" {
					_, n := utf8.DecodeLastRuneInString(v.text)
					v.text = v.text[:len(v.text)-n]
				}
				return false, nil
			}
			if k.Rune != '\r' && k.Rune != '\n' {
				if !unicode.IsControl(k.Rune) && utf8.RuneCountInString(v.text) < 4096 {
					v.text += string(k.Rune)
					if v.mode == wsSearch {
						v.query = v.text
						queueWorkspace(queries, v)
					}
				}
				return false, nil
			}
			mode, text, target := v.mode, strings.TrimSpace(v.text), v.target
			v.mode = wsNormal
			v.text = ""
			switch mode {
			case wsSearch:
				return false, nil
			case wsNewGroup:
				if text == "" {
					return false, nil
				}
				cwd, _ := os.Getwd()
				launch(func(ctx context.Context) workspaceOperation {
					var g terminal.Group
					e := c.DoContext(ctx, "POST", "/groups", terminal.GroupConfig{Name: text, CWD: cwd}, &g)
					return workspaceOperation{Group: &g, Err: e}
				})
			case wsRun:
				if text != "" {
					createTerminal(workspaceCustomLaunch(text, shell))
				}
			case wsRename:
				if text == "" {
					return false, nil
				}
				group := v.group == ""
				launch(func(ctx context.Context) workspaceOperation {
					path := "/terminals/" + target
					var payload any = terminal.TerminalUpdate{Name: &text}
					if group {
						path = "/groups/" + target
						payload = terminal.GroupUpdate{Name: &text}
					}
					e := c.DoContext(ctx, "PATCH", path, payload, nil)
					return workspaceOperation{Notice: "Renamed to " + text + ".", Err: e}
				})
			case wsResume:
				if text != "resume" {
					v.notice = "Nothing was resumed."
					return false, nil
				}
				for _, r := range v.rows {
					if string(r.ID) == target && r.External != nil {
						importAgent(r.External.Ref, true)
						break
					}
				}
			case wsConfirm:
				if text != "close" {
					v.notice = "Nothing was closed. Type close to confirm next time."
					return false, nil
				}
				group := v.group == ""
				launch(func(ctx context.Context) workspaceOperation {
					path := "/terminals/" + target
					if group {
						path = "/groups/" + target + "?force=true"
					}
					e := c.DoContext(ctx, "DELETE", path, nil, nil)
					return workspaceOperation{Notice: "Closed.", Err: e}
				})
			case wsCommand:
				command, arg, _ := strings.Cut(text, " ")
				arg = strings.TrimSpace(arg)
				switch command {
				case "q", "quit", "detach":
					return true, nil
				case "groups":
					v.toGroups()
					queueWorkspace(queries, v)
				case "new":
					if v.group == "" {
						v.mode = wsNewGroup
					} else {
						v.mode = wsLaunch
					}
				case "run":
					if v.group == "" {
						v.notice = "Open a group before starting a terminal."
					} else if arg != "" {
						createTerminal(workspaceCustomLaunch(arg, shell))
					} else {
						v.mode = wsRun
					}
				case "b", "open":
					shown := make([]terminal.Info, 0, len(v.rows))
					for _, r := range v.rows {
						shown = append(shown, terminal.Info{ID: r.ID, Name: r.Name})
					}
					selected, e := client.Find(shown, arg)
					if e != nil {
						v.notice = e.Error()
					} else {
						v.selected = string(selected.ID)
						return handle(workspaceKey{Rune: '\r'})
					}
				case "motion":
					v.motion = !v.motion
					if !v.motion {
						v.brandPhase = 0
					}
					v.notice = "Motion on."
					if !v.motion {
						v.notice = "Motion off."
					}
				case "ls":
					v.query = ""
					queueWorkspace(queries, v)
				default:
					v.notice = "Commands: groups, new, run command, b name, ls, motion, q."
				}
			}
			return false, nil
		}
		if k.Direction != 0 {
			v.move(k.Direction)
			queueWorkspace(queries, v)
			return false, nil
		}
		if strings.ContainsRune("rmxRc", k.Rune) || (k.Rune == 'a' && v.group == "") {
			if v.err != nil || v.snapshot == nil || !v.snapshot.Query.matches(v) {
				v.notice = "Wait for this view to refresh before changing it."
				return false, nil
			}
		}
		switch k.Rune {
		case 'j':
			v.move(1)
			queueWorkspace(queries, v)
		case 'k':
			v.move(-1)
			queueWorkspace(queries, v)
		case 'G':
			v.move(1 << 30)
			queueWorkspace(queries, v)
		case '0':
			v.move(-1 << 30)
			queueWorkspace(queries, v)
		case '/':
			v.searchBefore = v.query
			v.mode = wsSearch
			v.text = v.query
		case ':':
			v.mode = wsCommand
			v.text = ""
		case '?':
			v.mode = wsHelp
		case 'q':
			return true, nil

		case 'h':
			v.history = !v.history
			queueWorkspace(queries, v)
		case 'n':
			if v.group == "" {
				v.mode = wsNewGroup
			} else {
				v.mode = wsLaunch
			}
			v.text = ""
		case 'r':
			if v.selected != "" {
				v.target = v.selected
				v.mode = wsRename
				v.text = ""
			}
		case 'm':
			if v.group != "" && v.selected != "" {
				r, ok := v.row()
				if ok && r.External == nil && r.Local == nil {
					v.target = v.selected
					v.aux = 0
					v.mode = wsMove
				}
			}
		case 'x':
			if v.selected != "" {
				if r, ok := v.row(); v.group != "" && (!ok || r.External != nil || r.Local != nil) {
					v.notice = "This is an original tab; ptyd does not own its process."
				} else {
					v.target = v.selected
					v.mode = wsConfirm
					v.text = ""
				}
			}
		case 'a':
			if v.group != "" {
				v.outside = !v.outside
				queueWorkspace(queries, v)
			} else {
				for _, g := range v.groups {
					if string(g.ID) == v.selected {
						id := g.ID
						archived := !g.Archived
						launch(func(ctx context.Context) workspaceOperation {
							e := c.DoContext(ctx, "PATCH", "/groups/"+string(id), terminal.GroupUpdate{Archived: &archived}, nil)
							return workspaceOperation{Notice: "Updated group archive.", Err: e}
						})
						break
					}
				}
			}
		case 'R':
			r, ok := v.row()
			if v.group != "" && ok && r.External == nil && r.Local == nil && r.State != terminal.OverviewRunning {
				if r.Source.Provider != "" {
					importAgent(r.Source, false)
					return false, nil
				}
				id := string(r.ID)
				launch(func(ctx context.Context) workspaceOperation {
					var t terminal.Info
					e := c.DoContext(ctx, "POST", "/terminals/"+id+"/relaunch", map[string]string{"policy": "auto"}, &t)
					return workspaceOperation{Terminal: &t, Err: e}
				})
			}
		case 'v':
			if v.group != "" {
				if r, ok := v.row(); ok && r.External == nil && r.Local == nil {
					v.togglePin(r.ID)
					if e := layouts.save(v.group, v.watched()); e != nil {
						v.notice = e.Error()
					}
					queueWorkspace(queries, v)
				}
			}
		case 'c':
			if r, ok := v.row(); v.group != "" && ok && r.External == nil && r.Local == nil {
				group, id := v.group, r.ID
				notice := "Captain designated."
				if v.isCaptain(r) {
					id, notice = "", "Captain designation cleared."
				}
				launch(func(ctx context.Context) workspaceOperation {
					return workspaceOperation{Notice: notice, Err: chooseCaptain(ctx, c, group, id)}
				})
			}
		case '\r', '\n', 'i':
			if v.err != nil || v.snapshot == nil || !v.snapshot.Query.matches(v) {
				v.notice = "Wait for this view to refresh before opening it."
				return false, nil
			}
			if v.group == "" {
				if v.selected != "" {
					v.enterGroup(terminal.GroupID(v.selected))
					queueWorkspace(queries, v)
				}
				return false, nil
			}
			r, ok := v.row()
			if !ok {
				return false, nil
			}
			if r.Local != nil {
				v.notice = r.Local.Guidance()
				return false, nil
			}
			if r.External != nil {
				switch r.External.Action {
				case native.Attach:
					importAgent(r.External.Ref, false)
				case native.Resume:
					v.mode, v.target, v.text = wsResume, string(r.ID), ""
				default:
					v.notice = r.External.Reason
				}
				return false, nil
			}
			if r.HumanControl {
				v.notice = "Another person has control. Press v to pin a live pane."
				return false, nil
			}
			return false, attachTerminal(terminal.Info{ID: r.ID, Name: r.Name, GroupID: r.GroupID, Agent: r.Agent, Source: r.Source, Command: r.Command, HumanControl: r.HumanControl, Status: func() terminal.Status {
				if r.State == terminal.OverviewRunning {
					return terminal.StatusRunning
				}
				return terminal.StatusExited
			}()})
		}
		return false, nil
	}
	draw()
	for {
		var keys []workspaceKey
		select {
		case <-ctx.Done():
			return nil
		case result := <-updates:
			if saved := loadWorkspaceLayouts(c.Addr); saved.readErr == nil {
				v.captains = saved.captains
			}
			before := v.selected
			v.refresh(result)
			if before != v.selected || !result.Query.matches(v) {
				queueWorkspace(queries, v)
			}
			if autoOpen != "" && v.snapshot != nil && v.err == nil {
				for _, r := range v.rows {
					if string(r.ID) == autoOpen {
						v.selected = autoOpen
						autoOpen = ""
						_, err = handle(workspaceKey{Rune: '\r'})
						if err != nil {
							return err
						}
						break
					}
				}
			}
			draw()
			continue
		case result := <-ops:
			if result.Epoch != operationEpoch {
				continue
			}
			if operationCancel != nil {
				operationCancel()
				operationCancel = nil
			}
			v.busy = false
			v.notice = result.Notice
			if saved := loadWorkspaceLayouts(c.Addr); saved.readErr == nil {
				v.captains = saved.captains
			}
			if result.Err != nil {
				v.notice = result.Err.Error()
			} else if result.Group != nil {
				v.enterGroup(result.Group.ID)
				v.groups = append(v.groups, terminal.GroupSummary{Group: *result.Group})
				v.rows = nil
				v.snapshot = nil
				v.selected = ""
				v.query = ""
				v.history = true
			} else if result.Terminal != nil {
				t := *result.Terminal
				v.group = t.GroupID
				v.outside = false
				v.selected = string(t.ID)
				if err = attachTerminal(t); err != nil {
					return err
				}
			}
			queueWorkspace(queries, v)
			draw()
			continue
		case <-resize:
			draw()
			continue
		case now := <-tick.C:
			if v.motion && !now.Before(nextMotion) {
				v.brandPhase = (v.brandPhase + 1) % 32
				nextMotion = now.Add(workspaceMotionInterval)
				_, _ = io.WriteString(out, "\x1b[?2026h"+workspaceBrand(frameCols, frameRows, v.brandPhase, v.color)+"\x1b[?2026l")
			}
			// Complete queued mouse/key reports before treating a timed-out
			// prefix as Escape after a delayed draw.
			select {
			case chunk, ok := <-input.Bytes:
				if !ok {
					return nil
				}
				keys = parser.feed(chunk, now)
			default:
				keys = parser.expire(now)
			}
			if len(keys) == 0 {
				continue
			}
		case chunk, ok := <-input.Bytes:
			if !ok {
				return nil
			}
			keys = parser.feed(chunk, time.Now())
		}
		backOnly := false
		for _, k := range keys {
			// A repeated back chord may cross several levels in one read. Other
			// queued keys must not activate commands in the newly opened view.
			if backOnly && !k.Back && !k.Escape {
				continue
			}
			generation, group, mode := v.generation, v.group, v.mode
			quit, e := handle(k)
			if v.mode == wsSearch && v.query != v.text {
				v.query = v.text
				queueWorkspace(queries, v)
			}
			if e != nil {
				return e
			}
			if quit {
				return nil
			}
			if generation != v.generation {
				break
			}
			if (group != v.group || mode != v.mode) && (k.Back || k.Escape) {
				backOnly = true
			}
			if group != v.group {
				if !k.Back && !k.Escape {
					parser.lastJ = time.Time{}
					break
				}
			}
		}
		draw()
	}
}
