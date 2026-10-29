package client

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// SessionCaption combines agent identity with a useful display name. Titles are
// presentation metadata; they never identify a conversation for input or resume.
type SessionCaption struct {
	Name, Provider, Group string
	Captain               bool
}

func UsesTerminalTitle(name, provider string) bool {
	if provider != "codex" && provider != "claude" {
		return false
	}
	parts := strings.Fields(strings.ToLower(name))
	if len(parts) == 0 {
		return true
	}
	if parts[0] != provider || len(parts) > 2 {
		return false
	}
	if len(parts) == 1 {
		return true
	}
	for _, r := range parts[1] {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func (c SessionCaption) Title(title string) string {
	if UsesTerminalTitle(c.Name, c.Provider) {
		title = strings.TrimSpace(title)
		// Claude prefixes its OSC title with a rotating activity glyph.
		for title != "" {
			r, n := utf8.DecodeRuneInString(title)
			if !strings.ContainsRune("✳✻✶✽⏺●◐◓◑◒", r) && !(r >= 0x2800 && r <= 0x28ff) {
				break
			}
			next, _ := utf8.DecodeRuneInString(title[n:])
			if !unicode.IsSpace(next) {
				break
			}
			title = strings.TrimSpace(title[n:])
		}
		if title != "" && !strings.EqualFold(title, c.Provider) && !strings.EqualFold(title, "Claude Code") {
			return title
		}
	}
	return c.Name
}

func (c SessionCaption) Session(title string) string {
	name := c.Title(title)
	app := ""
	switch c.Provider {
	case "codex":
		app = "Codex"
	case "claude":
		app = "Claude"
	}
	if app == "" {
		return name
	}
	if name == "" || strings.EqualFold(name, app) {
		return app
	}
	return app + " · " + name
}

func (c SessionCaption) Label(title string) string {
	label := c.Session(title)
	if c.Captain {
		label = "★ " + label + " · Captain"
	}
	if c.Group != "" {
		label = c.Group + " / " + label
	}
	return label
}
