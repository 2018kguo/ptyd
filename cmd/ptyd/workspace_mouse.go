package main

import (
	"time"

	"github.com/2018kguo/ptyd/internal/client"
	"github.com/2018kguo/ptyd/internal/terminal"
)

type workspaceHitKind uint8

const (
	workspaceHitKey workspaceHitKind = iota
	workspaceHitGroup
	workspaceHitTerminal
	workspaceHitPane
	workspaceHitDestination
)

type workspaceHit struct {
	Rect  workspaceRect
	Kind  workspaceHitKind
	ID    string
	Group terminal.GroupID
	Key   workspaceKey
}
type workspaceScene struct {
	ANSI string
	Hits []workspaceHit
}
type workspaceClickTarget struct {
	Kind  workspaceHitKind
	ID    string
	Group terminal.GroupID
}
type workspaceClick struct {
	Target workspaceClickTarget
	At     time.Time
}
type workspaceMouse struct{ last workspaceClick }

func (m *workspaceMouse) click(event client.MouseEvent, hits []workspaceHit, now time.Time) (workspaceHit, bool, bool) {
	if !event.LeftPress() {
		if !event.Release && event.Button&32 == 0 {
			m.last = workspaceClick{}
		}
		return workspaceHit{}, false, false
	}
	x, y := event.X-1, event.Y-1
	// Later targets belong to overlays/controls drawn above earlier targets.
	for i := len(hits) - 1; i >= 0; i-- {
		h := hits[i]
		r := h.Rect
		if x < r.x || x >= r.x+r.w || y < r.y || y >= r.y+r.h {
			continue
		}
		target := workspaceClickTarget{h.Kind, h.ID, h.Group}
		if h.Kind == workspaceHitKey {
			m.last = workspaceClick{}
			return h, false, true
		}
		gap := now.Sub(m.last.At)
		double := m.last.Target == target && !m.last.At.IsZero() && gap >= 0 && gap < 350*time.Millisecond
		m.last = workspaceClick{target, now}
		if double {
			m.last = workspaceClick{}
		}
		return h, double, true
	}
	m.last = workspaceClick{}
	return workspaceHit{}, false, false
}
