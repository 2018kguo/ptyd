package terminal

import (
	"path/filepath"

	"github.com/2018kguo/ptyd/internal/vt"
)

// Screen is what a terminal's window shows, as text.
type Screen struct {
	Cols          int      `json:"cols"`
	Rows          int      `json:"rows"`
	CursorX       int      `json:"cursor_x"`
	CursorY       int      `json:"cursor_y"`
	CursorVisible bool     `json:"cursor_visible"`
	AltScreen     bool     `json:"alt_screen"`
	Title         string   `json:"title,omitempty"`
	Lines         []string `json:"lines"`
	Scrollback    []string `json:"scrollback,omitempty"`
}

func screenOf(s *vt.Terminal, withScrollback bool) Screen {
	cols, rows := s.Size()
	x, y, visible := s.Cursor()
	out := Screen{
		Cols: cols, Rows: rows,
		CursorX: x, CursorY: y, CursorVisible: visible,
		AltScreen: s.AltScreen(),
		Title:     s.Title(),
		Lines:     s.Lines(),
	}
	if withScrollback {
		out.Scrollback = s.ScrollbackLines()
	}
	return out
}

// Screen returns the terminal's current screen as text, optionally with
// the scrollback above it.
func (m *Manager) Screen(id ID, withScrollback bool) (Screen, error) {
	t, err := m.get(id)
	if err != nil {
		if scr, ok := m.loadGhostScreen(id); ok {
			return screenOf(scr, withScrollback), nil
		}
		return Screen{}, err
	}
	t.scrMu.Lock()
	defer t.scrMu.Unlock()
	return screenOf(t.screen, withScrollback), nil
}

// ScreenANSI returns a byte sequence that repaints the terminal's current
// screen (and, optionally, its scrollback) on a terminal of the same size.
func (m *Manager) ScreenANSI(id ID, withScrollback bool) ([]byte, error) {
	t, err := m.get(id)
	if err != nil {
		if scr, ok := m.loadGhostScreen(id); ok {
			return scr.Repaint(withScrollback), nil
		}
		return nil, err
	}
	t.scrMu.Lock()
	defer t.scrMu.Unlock()
	return t.screen.Repaint(withScrollback), nil
}

// loadGhostScreen rebuilds a ghost's saved screen from disk.
func (m *Manager) loadGhostScreen(id ID) (*vt.Terminal, bool) {
	if _, ok := m.GhostMeta(id); !ok || m.opts.PersistDir == "" {
		return nil, false
	}
	dir := filepath.Join(m.opts.PersistDir, string(id))
	meta, err := readMeta(dir)
	if err != nil {
		return nil, false
	}
	scr, _, err := loadScreen(dir, int(meta.Cols), int(meta.Rows))
	if err != nil {
		return nil, false
	}
	return scr, true
}

// ghostScreen returns the repaint of a ghost's saved screen, fitted to
// cols x rows if they are set (a ghost cannot be resized, and its window
// may be larger than the client's).
func (m *Manager) ghostScreen(id ID, cols, rows uint16) ([]byte, bool) {
	scr, ok := m.loadGhostScreen(id)
	if !ok {
		return nil, false
	}
	if cols > 0 && rows > 0 {
		scr.Resize(int(cols), int(rows))
	}
	return scr.Repaint(true), true
}
