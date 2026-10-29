package terminal

import (
	"github.com/2018kguo/phosphor"
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

func screenOf(s *phosphor.Terminal, withScrollback bool) Screen {
	cols, rows := s.Size()
	cur := s.Cursor()
	out := Screen{
		Cols: cols, Rows: rows,
		CursorX: cur.X, CursorY: cur.Y, CursorVisible: cur.Visible,
		AltScreen: s.Modes().AltScreen,
		Title:     s.Title(),
		Lines:     s.Lines(),
	}
	if withScrollback {
		out.Scrollback = s.ScrollbackLines()
	}
	return out
}

// readScreen serializes observation with PTY output without claiming a viewer.
func (m *Manager) readScreen(id ID, read func(*phosphor.Terminal)) error {
	t, err := m.get(id)
	if err != nil {
		if scr, ok := m.loadGhostScreen(id); ok {
			read(scr)
			return nil
		}
		return err
	}
	t.scrMu.Lock()
	defer t.scrMu.Unlock()
	read(t.screen)
	return nil
}

// Screen returns the terminal's current screen as text, optionally with history.
func (m *Manager) Screen(id ID, withScrollback bool) (out Screen, err error) {
	err = m.readScreen(id, func(s *phosphor.Terminal) { out = screenOf(s, withScrollback) })
	return
}

// ScreenANSI returns a repaint for a terminal of the same size.
func (m *Manager) ScreenANSI(id ID, withScrollback bool) (out []byte, err error) {
	err = m.readScreen(id, func(s *phosphor.Terminal) { out = s.Repaint(withScrollback) })
	return
}

// ScreenSnapshot reads exact cells, palettes and cluster modes without attaching.
func (m *Manager) ScreenSnapshot(id ID) (out []byte, err error) {
	err = m.readScreen(id, func(s *phosphor.Terminal) { out = s.Snapshot() })
	return
}

// loadGhostScreen rebuilds a ghost's saved screen from disk.
func (m *Manager) loadGhostScreen(id ID) (*phosphor.Terminal, bool) {
	if _, ok := m.GhostMeta(id); !ok {
		return nil, false
	}
	dir := terminalDir(m.opts.PersistDir, id)
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
