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
	readErr      error
}
type workspaceLayoutFile struct {
	Format int                                `json:"format"`
	Pins   map[terminal.GroupID][]terminal.ID `json:"pins"`
}

func readWorkspacePins(path string) (map[terminal.GroupID][]terminal.ID, error) {
	pins := map[terminal.GroupID][]terminal.ID{}
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return pins, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, (64<<10)+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > 64<<10 {
		return nil, fmt.Errorf("pane layout exceeds 64 KiB")
	}
	var saved workspaceLayoutFile
	if err = json.Unmarshal(raw, &saved); err != nil {
		return nil, err
	}
	if saved.Format != 1 {
		return nil, fmt.Errorf("unknown pane layout format")
	}
	for group, ids := range saved.Pins {
		for _, id := range ids {
			if id != "" && len(pins[group]) < maxWorkspacePins && !slices.Contains(pins[group], id) {
				pins[group] = append(pins[group], id)
			}
		}
	}
	return pins, nil
}

func loadWorkspaceLayouts(addr string) *workspaceLayouts {
	l := &workspaceLayouts{pins: map[terminal.GroupID][]terminal.ID{}}
	token, err := client.TokenFile(addr)
	if err == nil {
		digest := sha256.Sum256([]byte(addr))
		l.path = filepath.Join(filepath.Dir(token), "view-"+hex.EncodeToString(digest[:6])+".json")
		var pins map[terminal.GroupID][]terminal.ID
		pins, err = readWorkspacePins(l.path)
		if err == nil {
			l.pins = pins
		}
	}
	if err != nil {
		l.readErr = err
		l.notice = "Saved pane layout could not be read; current changes will stay in this view."
	}
	return l
}

func (l *workspaceLayouts) save(group terminal.GroupID, ids []terminal.ID) error {
	if l.readErr != nil {
		return fmt.Errorf("pane is updated in this view; fix or remove %s and reopen the browser to save layouts", l.path)
	}
	if len(ids) == 0 {
		delete(l.pins, group)
	} else {
		l.pins[group] = append([]terminal.ID(nil), ids...)
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
		return fmt.Errorf("another browser is saving panes; this change stays in this view")
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	// Merge only this group's edit while locked. Another browser may have
	// saved a different group since this view opened.
	pins, err := readWorkspacePins(l.path)
	if err != nil {
		return fmt.Errorf("could not read pane layout; this change stays in this view: %w", err)
	}
	if len(ids) == 0 {
		delete(pins, group)
	} else {
		pins[group] = append([]terminal.ID(nil), ids...)
	}
	raw, err := json.Marshal(workspaceLayoutFile{Format: 1, Pins: pins})
	if err != nil {
		return err
	}
	if len(raw) > 64<<10 {
		return fmt.Errorf("pane layout is full; this view still keeps your pins")
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
