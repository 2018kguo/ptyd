package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"syscall"

	"github.com/2018kguo/ptyd/internal/client"
	"github.com/2018kguo/ptyd/internal/terminal"
)

type workspaceLayouts struct {
	path, notice string
	pins         map[terminal.GroupID][]terminal.ID
	captains     map[terminal.GroupID]terminal.ID
	readErr      error
}
type workspaceLayoutFile struct {
	Format   int                                `json:"format"`
	Pins     map[terminal.GroupID][]terminal.ID `json:"pins"`
	Captains map[terminal.GroupID]terminal.ID   `json:"captains,omitempty"`
}

func readWorkspaceLayout(path string) (workspaceLayoutFile, error) {
	saved := workspaceLayoutFile{Pins: map[terminal.GroupID][]terminal.ID{}, Captains: map[terminal.GroupID]terminal.ID{}}
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		saved.Format = 2
		return saved, nil
	}
	if err != nil {
		return saved, err
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, (64<<10)+1))
	if err != nil {
		return saved, err
	}
	if len(raw) > 64<<10 {
		return saved, fmt.Errorf("workspace layout exceeds 64 KiB")
	}
	if err = json.Unmarshal(raw, &saved); err != nil {
		return saved, err
	}
	if saved.Format != 1 && saved.Format != 2 {
		return saved, fmt.Errorf("unknown workspace layout format")
	}
	// Older clients refuse this format instead of dropping captain choices
	// when saving their pins. Existing pin-only layouts still read directly.
	saved.Format = 2
	pins := map[terminal.GroupID][]terminal.ID{}
	for group, ids := range saved.Pins {
		for _, id := range ids {
			if id != "" && len(pins[group]) < maxWorkspacePins && !slices.Contains(pins[group], id) {
				pins[group] = append(pins[group], id)
			}
		}
	}
	saved.Pins = pins
	if saved.Captains == nil {
		saved.Captains = map[terminal.GroupID]terminal.ID{}
	}
	return saved, nil
}

func loadWorkspaceLayouts(addr string) *workspaceLayouts {
	l := &workspaceLayouts{pins: map[terminal.GroupID][]terminal.ID{}, captains: map[terminal.GroupID]terminal.ID{}}
	token, err := client.TokenFile(addr)
	if err == nil {
		digest := sha256.Sum256([]byte(addr))
		l.path = filepath.Join(filepath.Dir(token), "view-"+hex.EncodeToString(digest[:6])+".json")
		var saved workspaceLayoutFile
		saved, err = readWorkspaceLayout(l.path)
		if err == nil {
			l.pins, l.captains = saved.Pins, saved.Captains
		}
	}
	if err != nil {
		l.readErr = err
		l.notice = "Saved workspace layout could not be read; current changes will stay in this view."
	}
	return l
}

func (l *workspaceLayouts) save(group terminal.GroupID, ids []terminal.ID) error {
	if len(ids) == 0 {
		delete(l.pins, group)
	} else {
		l.pins[group] = append([]terminal.ID(nil), ids...)
	}
	return l.write(func(saved *workspaceLayoutFile) {
		if len(ids) == 0 {
			delete(saved.Pins, group)
		} else {
			saved.Pins[group] = append([]terminal.ID(nil), ids...)
		}
	})
}

func (l *workspaceLayouts) saveCaptain(group terminal.GroupID, id terminal.ID) error {
	err := l.write(func(saved *workspaceLayoutFile) {
		if id == "" {
			delete(saved.Captains, group)
		} else {
			saved.Captains[group] = id
		}
	})
	if err == nil {
		l.captains[group] = id
	}
	return err
}

func (l *workspaceLayouts) write(edit func(*workspaceLayoutFile)) error {
	if l.readErr != nil {
		return fmt.Errorf("fix or remove %s and reopen the browser to save workspace settings: %w", l.path, l.readErr)
	}
	dir := filepath.Dir(l.path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	lock, err := os.OpenFile(l.path+".lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return fmt.Errorf("another client is saving workspace settings; try again")
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	// Merge only this group's edit while locked. Another browser may have
	// saved a different group since this view opened.
	saved, err := readWorkspaceLayout(l.path)
	if err != nil {
		return fmt.Errorf("could not read pane layout; this change stays in this view: %w", err)
	}
	edit(&saved)
	raw, err := json.Marshal(saved)
	if err != nil {
		return err
	}
	if len(raw) > 64<<10 {
		return fmt.Errorf("workspace settings exceed 64 KiB; remove unused pins or captain choices")
	}
	f, err := os.CreateTemp(dir, ".view-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(raw); err == nil {
		err = f.Close()
	} else {
		_ = f.Close()
	}
	if err == nil {
		err = os.Rename(f.Name(), l.path)
	}
	if err != nil {
		return fmt.Errorf("could not save pane layout: %w", err)
	}
	return nil
}
