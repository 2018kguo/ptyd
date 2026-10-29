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

	"github.com/2018kguo/phosphor"
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
	Group    terminal.GroupID
	Selected string
	Search   string
	History  bool
	Outside  bool
	Watches  []terminal.ID
}
type workspaceSnapshot struct {
	Query      workspaceQuery
	Groups     []terminal.GroupSummary
	Overview   terminal.Overview
	Preview    *phosphor.Terminal
	PreviewErr error
	PreviewID  string
	Pinned     []workspacePinnedPreview
	Err        error
}
type workspaceView struct {
	group                   terminal.GroupID
	selected                string
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
	searchBefore            string
	motion                  bool
	brandPhase              int
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
		if v.query != "" && !strings.Contains(strings.ToLower(g.Name+" "+g.CWD), strings.ToLower(v.query)) {
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
	for _, p := range s.Pinned {
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
	return q.Group == v.group && q.Search == v.query && q.History == v.history && q.Outside == v.outside && slices.Equal(q.Watches, v.watched())
}
func queueWorkspace(ch chan workspaceQuery, v workspaceView) {
	q := workspaceQuery{Group: v.group, Selected: v.selected, Search: v.query, History: v.history, Outside: v.outside, Watches: append([]terminal.ID(nil), v.watched()...)}
	select {
	case <-ch:
	default:
	}
	ch <- q
}
func refreshWorkspace(ctx context.Context, c client.API, queries <-chan workspaceQuery, updates chan workspaceSnapshot) {
	ticker := time.NewTicker(750 * time.Millisecond)
	defer ticker.Stop()
	q := workspaceQuery{}
	for {
		select {
		case q = <-queries:
		case <-ctx.Done():
			return
		}
		for {
			budget := 3 * time.Second
			if q.Outside {
				budget = 12 * time.Second
			}
			req, cancel := context.WithTimeout(ctx, budget)
			s := workspaceSnapshot{Query: q}
			s.Groups, s.Err = c.Groups(req)
			group := q.Group
			home := group == ""
			if home {
				group = terminal.GroupID(q.Selected)
			}
			if s.Err == nil && group != "" {
				options := terminal.OverviewOptions{GroupID: group, Limit: terminal.MaxOverviewLimit, Discover: q.Outside, ActiveOnly: !q.History}
				if !home {
					options.Query = q.Search
				} else {
					options.ActiveOnly = false
				}
				s.Overview, s.Err = c.Overview(req, options)
				if home && s.Err != nil {
					s.PreviewErr = s.Err
					s.Err = nil
				}
				if s.Err == nil {
					rows := s.Overview.Sessions
					if home {
						for i, r := range rows {
							if r.Attention > 0 || r.State == terminal.OverviewRunning {
								rows = append([]terminal.SessionSummary{r}, rows[:i]...)
								break
							}
						}
					}
					for _, r := range rows {
						if (home || string(r.ID) == q.Selected) && r.External == nil && r.Local == nil {
							s.PreviewID = string(r.ID)
							// Screens are fetched together below.
							break
						}
					}
				}
			}
			if s.Err == nil {
				fetchWorkspaceScreens(req, c, &s)
			}
			cancel()
			select {
			case <-updates:
			default:
			}
			select {
			case updates <- s:
			case <-ctx.Done():
				return
			}
			select {
			case q = <-queries:
				continue
			case <-ticker.C:
				continue
			case <-ctx.Done():
				return
			}
		}
	}
}

type workspaceKey struct {
	Rune      rune
	Direction int
	Escape    bool
	Paste     bool
	Back      bool
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
			paste := bytes.Equal(p.pending[:n], []byte("\x1b[200~"))
			p.pending = p.pending[n:]
			p.since = time.Time{}
			if paste {
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
		_, _ = io.WriteString(out, "\x18"+"\x1b[?2026l\x1b[?2004l\x1b[?7h\x1b[0m\x1b[?25h\x1b[?1049l\x1b[23;0t")
	}()
	colors, pending := client.ProbeColors(ctx, in, out)
	input, err := client.NewInput(ctx, in, pending)
	if err != nil {
		return err
	}
	defer func() { input.Close() }()
	_, _ = io.WriteString(out, "\x1b[22;0t"+statusEnter)
	history := start != "" || (len(showHistory) > 0 && showHistory[0])
	layouts := loadWorkspaceLayouts(c.Addr)
	v := workspaceView{pins: layouts.pins, motion: os.Getenv("PTYD_REDUCED_MOTION") == "", notice: layouts.notice, history: history, group: start, selected: string(initial), color: os.Getenv("NO_COLOR") == "" && os.Getenv("TERM") != "dumb"}
	queries := make(chan workspaceQuery, 1)
	updates := make(chan workspaceSnapshot, 1)
	go refreshWorkspace(ctx, c, queries, updates)
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
	nextMotion := time.Now().Add(workspaceMotionInterval)
	draw := func() {
		cols, rows, e := term.GetSize(int(out.Fd()))
		if e != nil {
			cols, rows = 80, 24
		}
		frameCols, frameRows = cols, rows
		stable := v
		stable.brandPhase = 0
		frame := workspaceFrame(stable, cols, rows)
		if frame != lastFrame {
			if v.brandPhase != 0 {
				_, _ = io.WriteString(out, workspaceFrame(v, cols, rows))
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
	createTerminal := func(command string) {
		g, ok := v.groupInfo()
		if !ok {
			return
		}
		cfg := terminal.CreateConfig{GroupID: g.ID, CWD: g.CWD, Restore: terminal.RestoreAuto, TerminalColors: colors}
		switch command {
		case "codex", "claude":
			cfg.Command, cfg.Name = command, command
		case "shell":
			cfg.Command = os.Getenv("SHELL")
			if cfg.Command == "" {
				cfg.Command = "/bin/sh"
			}
			cfg.Name = "shell"
		default:
			cfg.Command = os.Getenv("SHELL")
			if cfg.Command == "" {
				cfg.Command = "/bin/sh"
			}
			cfg.Args = []string{"-lc", command}
			cfg.Name = clipStatus(command, 40)
			cfg.Restore = terminal.RestoreNone
		}
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
	attachGeneration := 0
	attachTerminal := func(info terminal.Info) error {
		attachGeneration++
		lastFrame = ""
		input.Close()
		_ = client.FlushInput(in)
		_ = term.Restore(int(in.Fd()), raw)
		raw = nil
		_, _ = io.WriteString(out, "\x1b[?2004l\x1b[0m\x1b[?25h\x1b[?1049l")
		label := info.Name
		if g, ok := v.groupInfo(); ok {
			label = g.Name + " / " + info.Name
		}
		result, attachErr := client.Attach(ctx, client.Options{
			Addr: c.Addr, Token: c.Token, ID: string(info.ID), Label: label,
			Human: true, Hold: true, ReadOnly: info.Status != terminal.StatusRunning,
		}, in, out, resize)
		var unread []byte
		if result.Detached && attachErr == nil {
			unread = result.PendingInput
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		raw, err = term.MakeRaw(int(in.Fd()))
		if err != nil {
			return err
		}
		input, err = client.NewInput(ctx, in, unread)
		if err != nil {
			return err
		}
		parser = workspaceInput{}
		_, _ = io.WriteString(out, statusEnter)
		v.selected = string(info.ID)
		v.notice = "Returned to your workspace."
		if attachErr != nil {
			v.notice = "Could not open: " + attachErr.Error()
		}
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
			case 'c':
				v.mode = wsNormal
				createTerminal("codex")
			case 'a':
				v.mode = wsNormal
				createTerminal("claude")
			case 's':
				v.mode = wsNormal
				createTerminal("shell")
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
					createTerminal(text)
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
						createTerminal(arg)
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
		if strings.ContainsRune("rmxR", k.Rune) || (k.Rune == 'a' && v.group == "") {
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
			return false, attachTerminal(terminal.Info{ID: r.ID, Name: r.Name, GroupID: r.GroupID, HumanControl: r.HumanControl, Status: func() terminal.Status {
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
			keys = parser.expire(now)
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
			generation, group, mode := attachGeneration, v.group, v.mode
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
			if generation != attachGeneration {
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
