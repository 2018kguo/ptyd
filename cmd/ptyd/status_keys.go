package main

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/2018kguo/ptyd/internal/client"
	"github.com/2018kguo/ptyd/internal/terminal"
)

type statusMode uint8

const (
	statusNormal statusMode = iota
	statusSearch
	statusCommand
	statusHelp
)

type statusAction uint8

const (
	statusStay statusAction = iota
	statusQuit
	statusAttach
	statusRefresh
)

func (v *statusView) cancelPrompt() {
	v.mode, v.command, v.gotoFirst = statusNormal, "", false
}

// key changes only local view state. The UI loop owns requests and attachment.
func (v *statusView) key(r rune) statusAction {
	if r == 0x1b {
		v.cancelPrompt()
		return statusStay
	}
	if r == 3 {
		return statusQuit
	}
	switch v.mode {
	case statusHelp:
		if r == 'q' || r == '?' || r == '\r' || r == '\n' {
			v.cancelPrompt()
		}
		return statusStay
	case statusSearch, statusCommand:
		text := &v.query
		if v.mode == statusCommand {
			text = &v.command
		}
		switch r {
		case '\r', '\n':
			if v.mode == statusCommand {
				return v.runCommand()
			}
			v.mode = statusNormal
		case 0x7f, 0x08:
			if *text != "" {
				_, n := utf8.DecodeLastRuneInString(*text)
				*text = (*text)[:len(*text)-n]
			}
		default:
			if !unicode.IsControl(r) && utf8.RuneCountInString(*text) < 200 {
				*text += string(r)
			}
		}
		return statusStay
	}
	if r == 'g' {
		if v.gotoFirst {
			v.move(-len(v.sessions()))
			v.gotoFirst = false
		} else {
			v.gotoFirst = true
		}
		return statusStay
	}
	v.gotoFirst = false
	switch r {
	case 'j':
		v.move(1)
	case 'k':
		v.move(-1)
	case 'G':
		v.move(len(v.sessions()))
	case '/':
		v.mode, v.query = statusSearch, ""
	case ':':
		v.mode, v.command = statusCommand, ""
	case '?':
		v.mode = statusHelp
	case 'q':
		return statusQuit
	case 'r':
		return statusRefresh
	case 'i', '\r', '\n':
		return statusAttach
	}
	return statusStay
}

func (v *statusView) runCommand() statusAction {
	command := strings.TrimSpace(v.command)
	verb, arg, _ := strings.Cut(command, " ")
	arg = strings.TrimSpace(arg)
	v.cancelPrompt()
	v.notice = ""
	switch {
	case (verb == "q" || verb == "quit") && arg == "":
		return statusQuit
	case (verb == "help" || verb == "h") && arg == "":
		v.mode = statusHelp
	case verb == "ls" && arg == "":
		v.query = ""
		return statusRefresh
	case (verb == "bn" || verb == "bp") && arg == "":
		delta := 1
		if verb == "bp" {
			delta = -1
		}
		if n := len(v.sessions()); n > 0 {
			v.selected = v.sessions()[(v.selectedIndex()+delta+n)%n].ID
		}
		return statusAttach
	case verb == "b":
		if arg == "" {
			return statusAttach
		}
		// Resolve exactly within the displayed, bounded snapshot; never guess
		// from a title fragment or silently attach the selected fallback row.
		infos := make([]terminal.Info, 0, len(v.sessions()))
		for _, row := range v.sessions() {
			infos = append(infos, terminal.Info{ID: row.ID, Name: row.Name})
		}
		info, err := client.Find(infos, arg)
		if err != nil {
			v.notice = fmt.Sprintf("%v. :ls clears the filter; :b targets shown sessions.", err)
			return statusStay
		}
		v.selected = info.ID
		return statusAttach
	default:
		v.notice = "Unknown command. Use :b name, :bn, :bp, :ls, :q or :help."
	}
	return statusStay
}
