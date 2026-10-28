package terminal

import (
	"cmp"
	"path/filepath"
	"slices"
	"strings"
	"unicode/utf8"
)

const (
	DefaultOverviewLimit = 50
	MaxOverviewLimit     = 500
	overviewPreviewRunes = 160
)

// OverviewOptions select a bounded view of the canonical session list.
type OverviewOptions struct {
	Limit int
	Query string
	Tags  []string
}

type OverviewCounts struct {
	Total     int `json:"total"`
	Running   int `json:"running"`
	Exited    int `json:"exited"`
	Stopped   int `json:"stopped"`
	Attention int `json:"attention"`
}

// OverviewPreview describes observed output, never an inferred agent state.
type OverviewPreview struct {
	Source    string `json:"source"`
	Text      string `json:"text"`
	Truncated bool   `json:"truncated"`
}

type OverviewState string

const (
	OverviewRunning OverviewState = "running"
	OverviewStopped OverviewState = "stopped"
	OverviewExited  OverviewState = "exited"
)

// SessionSummary leaves arguments, watch definitions and transcripts on the
// detail routes. LatestSeq lets an agent continue incremental output reads.
type SessionSummary struct {
	ID          ID               `json:"id"`
	Name        string           `json:"name"`
	CWD         string           `json:"cwd"`
	Command     string           `json:"command"`
	Agent       string           `json:"agent,omitempty"`
	State       OverviewState    `json:"state"`
	ExitCode    *int             `json:"exit_code,omitempty"`
	IdleSeconds float64          `json:"idle_seconds"`
	Attached    int              `json:"attached"`
	LatestSeq   int64            `json:"latest_seq"`
	Attention   int              `json:"attention"`
	Preview     *OverviewPreview `json:"preview,omitempty"`
}

type Overview struct {
	Counts    OverviewCounts   `json:"counts"`
	Sessions  []SessionSummary `json:"sessions"`
	Truncated bool             `json:"truncated"`
}

// Overview reads the existing registry once; it creates no session state.
func (m *Manager) Overview(options OverviewOptions) Overview {
	m.mu.RLock()
	infos := make([]Info, 0, len(m.terms)+len(m.ghosts))
	for _, t := range m.terms {
		infos = append(infos, t.memorySnapshot())
	}
	for _, info := range m.ghosts {
		infos = append(infos, info)
	}
	m.mu.RUnlock()
	return BuildOverview(infos, options)
}

// BuildOverview is shared by the API and tests. Counts cover every matching
// session even when only the first Limit summaries are returned.
func BuildOverview(infos []Info, options OverviewOptions) Overview {
	limit := options.Limit
	if limit <= 0 {
		limit = DefaultOverviewLimit
	}
	limit = min(limit, MaxOverviewLimit)
	query := strings.ToLower(strings.TrimSpace(options.Query))
	indices := make([]int, 0, min(len(infos), limit))
	var out Overview
	for i, info := range infos {
		if !overviewMatches(info, query, options.Tags) {
			continue
		}
		out.Counts.Total++
		if len(info.Attention) > 0 {
			out.Counts.Attention++
		}
		switch overviewState(info) {
		case OverviewRunning:
			out.Counts.Running++
		case OverviewExited:
			out.Counts.Exited++
		case OverviewStopped:
			out.Counts.Stopped++
		}
		indices = append(indices, i)
	}
	slices.SortFunc(indices, func(a, b int) int {
		x, y := infos[a], infos[b]
		if d := cmp.Compare(overviewPriority(x), overviewPriority(y)); d != 0 {
			return d
		}
		if !x.CreatedAt.Equal(y.CreatedAt) {
			return x.CreatedAt.Compare(y.CreatedAt)
		}
		return strings.Compare(string(x.ID), string(y.ID))
	})
	out.Truncated = len(indices) > limit
	indices = indices[:min(len(indices), limit)]
	out.Sessions = make([]SessionSummary, 0, len(indices))
	for _, i := range indices {
		info := infos[i]
		out.Sessions = append(out.Sessions, SessionSummary{
			ID: info.ID, Name: info.Name, CWD: info.CWD,
			Command: filepath.Base(info.Command), Agent: info.Agent,
			State: overviewState(info), ExitCode: info.ExitCode, IdleSeconds: info.IdleSeconds,
			Attached: info.Attached, LatestSeq: info.LatestSeq, Attention: len(info.Attention),
			Preview: overviewPreview(info),
		})
	}
	return out
}

func overviewMatches(info Info, query string, tags []string) bool {
	if len(tags) > 0 && !AnyTag(info.Tags, tags) {
		return false
	}
	if query == "" {
		return true
	}
	for _, field := range []string{string(info.ID), info.Name, info.CWD, info.Command, info.Agent} {
		if strings.Contains(strings.ToLower(field), query) {
			return true
		}
	}
	return false
}

func overviewState(info Info) OverviewState {
	if info.Status == StatusRunning {
		return OverviewRunning
	}
	if info.ExitCode == nil {
		return OverviewStopped
	}
	return OverviewExited
}

func overviewPriority(info Info) int {
	switch {
	case len(info.Attention) > 0:
		return 0
	case info.Status != StatusRunning && info.Notice != "":
		return 1
	case info.Status == StatusRunning:
		return 2
	default:
		return 3
	}
}

func overviewPreview(info Info) *OverviewPreview {
	var source, text string
	switch {
	case len(info.Attention) > 0:
		source, text = "watch", info.Attention[len(info.Attention)-1].Line.Text
	case info.Notice != "":
		source, text = "notice", info.Notice
	case info.Pending != "":
		source, text = "pending", info.Pending
	default:
		return nil
	}
	truncated := false
	offset := 0
	for range overviewPreviewRunes {
		if offset == len(text) {
			break
		}
		_, n := utf8.DecodeRuneInString(text[offset:])
		offset += n
	}
	if offset < len(text) {
		text, truncated = text[:offset], true
	}
	return &OverviewPreview{Source: source, Text: text, Truncated: truncated}
}
