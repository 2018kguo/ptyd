package terminal

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

type GroupID string

const DefaultGroup GroupID = "default"

var (
	ErrGroupNotFound = errors.New("group not found")
	ErrGroupConflict = errors.New("group conflict")
)

// A group is a persistent workspace, independent of its terminals or creator.
type Group struct {
	ID        GroupID   `json:"id"`
	Name      string    `json:"name"`
	CWD       string    `json:"cwd"`
	CreatedAt time.Time `json:"created_at"`
	Archived  bool      `json:"archived"`
}
type GroupConfig struct {
	Name string `json:"name"`
	CWD  string `json:"cwd"`
}
type GroupUpdate struct {
	Name     *string `json:"name,omitempty"`
	Archived *bool   `json:"archived,omitempty"`
}
type GroupSummary struct {
	Group
	Terminals    int        `json:"terminals"`
	Running      int        `json:"running"`
	Attention    int        `json:"attention"`
	Viewing      int        `json:"viewing"`
	LastOutputAt *time.Time `json:"last_output_at,omitempty"`
}
type TerminalUpdate struct {
	Name    *string  `json:"name,omitempty"`
	GroupID *GroupID `json:"group_id,omitempty"`
}
type groupFile struct {
	Format int     `json:"format"`
	Groups []Group `json:"groups"`
}

func cleanGroupName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || !utf8.ValidString(name) || len(name) > maxNameLen {
		return "", fmt.Errorf("choose a group name between 1 and %d bytes", maxNameLen)
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return "", fmt.Errorf("group names cannot contain control characters")
		}
	}
	return name, nil
}
func (m *Manager) initGroups() {
	m.groups = map[GroupID]Group{DefaultGroup: {ID: DefaultGroup, Name: "Inbox", CreatedAt: m.startedAt}}
	if m.opts.PersistDir != "" {
		raw, err := os.ReadFile(filepath.Join(m.opts.PersistDir, "groups.json"))
		if err == nil {
			var file groupFile
			if err = json.Unmarshal(raw, &file); err == nil && file.Format != 1 {
				err = fmt.Errorf("unsupported group metadata format")
			}
			if err != nil {
				m.groupErr = fmt.Errorf("cannot read groups.json; restore its backup before changing groups: %w", err)
				return
			}
			seen := map[string]bool{}
			ids := map[GroupID]bool{}
			for _, g := range file.Groups {
				name, e := cleanGroupName(g.Name)
				if e != nil || g.ID == "" || seen[strings.ToLower(name)] {
					m.groupErr = fmt.Errorf("invalid or duplicated group metadata; restore groups.json from backup")
					return
				}
				if ids[g.ID] {
					m.groupErr = fmt.Errorf("duplicated group id in groups.json")
					return
				}
				ids[g.ID] = true
				g.Name = name
				seen[strings.ToLower(name)] = true
				m.groups[g.ID] = g
			}
			if !ids[DefaultGroup] && seen["inbox"] {
				m.groupErr = fmt.Errorf("Inbox name is reserved for the default group; restore groups.json from backup")
				return
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			m.groupErr = err
			return
		}
	}
	for id, g := range m.ghosts {
		if g.GroupID == "" {
			g.GroupID = DefaultGroup
			m.ghosts[id] = g
		}
		if _, ok := m.groups[g.GroupID]; !ok {
			name := "Recovered " + string(g.GroupID)
			for suffix := 2; ; suffix++ {
				duplicate := false
				for _, existing := range m.groups {
					if strings.EqualFold(name, existing.Name) {
						duplicate = true
						break
					}
				}
				if !duplicate {
					break
				}
				name = fmt.Sprintf("Recovered %s (%d)", g.GroupID, suffix)
			}
			m.groups[g.GroupID] = Group{ID: g.GroupID, Name: name, CWD: g.CWD, CreatedAt: g.CreatedAt}
		}
	}
	if err := m.persistGroups(m.groups); err != nil {
		m.groupErr = fmt.Errorf("cannot save group metadata: %w", err)
	}
}
func (m *Manager) persistGroups(groups map[GroupID]Group) error {
	if m.opts.PersistDir == "" {
		return nil
	}
	file := groupFile{Format: 1, Groups: make([]Group, 0, len(groups))}
	for _, g := range groups {
		file.Groups = append(file.Groups, g)
	}
	slices.SortFunc(file.Groups, func(a, b Group) int { return strings.Compare(string(a.ID), string(b.ID)) })
	return writeJSONAtomic(filepath.Join(m.opts.PersistDir, "groups.json"), file)
}
func (m *Manager) groupLocked(id GroupID) (Group, error) {
	if m.groupErr != nil {
		return Group{}, m.groupErr
	}
	if id == "" {
		id = DefaultGroup
	}
	g, ok := m.groups[id]
	if !ok {
		return Group{}, fmt.Errorf("%w: %s; list groups and choose an existing id", ErrGroupNotFound, id)
	}
	return g, nil
}
func (m *Manager) Group(id GroupID) (Group, error) {
	m.workspaceMu.RLock()
	defer m.workspaceMu.RUnlock()
	return m.groupLocked(id)
}
func (m *Manager) Groups() ([]GroupSummary, error) {
	m.workspaceMu.RLock()
	defer m.workspaceMu.RUnlock()
	if m.groupErr != nil {
		return nil, m.groupErr
	}
	counts := make(map[GroupID]*GroupSummary, len(m.groups))
	for id, g := range m.groups {
		counts[id] = &GroupSummary{Group: g}
	}
	for _, t := range m.List() {
		s := counts[t.GroupID]
		if s == nil {
			continue
		}
		s.Terminals++
		s.Viewing += t.Attached
		if t.Status == StatusRunning {
			s.Running++
		}
		if len(t.Attention) > 0 {
			s.Attention++
		}
		if t.LastOutputAt != nil && (s.LastOutputAt == nil || t.LastOutputAt.After(*s.LastOutputAt)) {
			v := *t.LastOutputAt
			s.LastOutputAt = &v
		}
	}
	out := make([]GroupSummary, 0, len(counts))
	for _, s := range counts {
		out = append(out, *s)
	}
	slices.SortFunc(out, func(a, b GroupSummary) int {
		if a.Archived != b.Archived {
			if a.Archived {
				return 1
			}
			return -1
		}
		return cmp.Or(strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)), strings.Compare(string(a.ID), string(b.ID)))
	})
	return out, nil
}
func (m *Manager) CreateGroup(cfg GroupConfig) (Group, error) {
	m.workspaceMu.Lock()
	defer m.workspaceMu.Unlock()
	if m.groupErr != nil {
		return Group{}, m.groupErr
	}
	name, err := cleanGroupName(cfg.Name)
	if err != nil {
		return Group{}, err
	}
	for _, g := range m.groups {
		if strings.EqualFold(g.Name, name) {
			return Group{}, fmt.Errorf("%w: group %q already exists; attach it instead", ErrGroupConflict, name)
		}
	}
	if cfg.CWD == "" {
		cfg.CWD, err = os.Getwd()
		if err != nil {
			return Group{}, err
		}
	}
	if !filepath.IsAbs(cfg.CWD) {
		return Group{}, fmt.Errorf("group directory must be absolute")
	}
	info, err := os.Stat(cfg.CWD)
	if err != nil || !info.IsDir() {
		return Group{}, fmt.Errorf("group directory does not exist or is not a directory")
	}
	g := Group{ID: GroupID("group-" + randHex(6)), Name: name, CWD: cfg.CWD, CreatedAt: time.Now()}
	next := cloneGroups(m.groups)
	next[g.ID] = g
	if err := m.persistGroups(next); err != nil {
		return Group{}, err
	}
	m.groups = next
	return g, nil
}
func cloneGroups(groups map[GroupID]Group) map[GroupID]Group {
	out := make(map[GroupID]Group, len(groups))
	for id, g := range groups {
		out[id] = g
	}
	return out
}
func (m *Manager) UpdateGroup(id GroupID, update GroupUpdate) (Group, error) {
	m.workspaceMu.Lock()
	defer m.workspaceMu.Unlock()
	g, err := m.groupLocked(id)
	if err != nil {
		return Group{}, err
	}
	if update.Name != nil {
		name, err := cleanGroupName(*update.Name)
		if err != nil {
			return Group{}, err
		}
		for key, other := range m.groups {
			if key != g.ID && strings.EqualFold(other.Name, name) {
				return Group{}, fmt.Errorf("%w: that group name is already in use", ErrGroupConflict)
			}
		}
		g.Name = name
	}
	if update.Archived != nil {
		if *update.Archived {
			if g.ID == DefaultGroup {
				return Group{}, fmt.Errorf("%w: Inbox receives ungrouped terminals and cannot be archived", ErrGroupConflict)
			}
			for _, t := range m.List() {
				if t.GroupID == g.ID && t.Status == StatusRunning {
					return Group{}, fmt.Errorf("%w: this group still has running terminals; stop them before archiving", ErrGroupConflict)
				}
			}
		}
		g.Archived = *update.Archived
	}
	next := cloneGroups(m.groups)
	next[g.ID] = g
	if err := m.persistGroups(next); err != nil {
		return Group{}, err
	}
	m.groups = next
	return g, nil
}
func (m *Manager) RemoveGroup(id GroupID, force bool) error {
	m.workspaceMu.Lock()
	defer m.workspaceMu.Unlock()
	g, err := m.groupLocked(id)
	if err != nil {
		return err
	}
	if g.ID == DefaultGroup {
		return fmt.Errorf("%w: Inbox is the default group and cannot be removed", ErrGroupConflict)
	}
	var ids []ID
	for _, t := range m.List() {
		if t.GroupID == id {
			ids = append(ids, t.ID)
		}
	}
	if len(ids) > 0 && !force {
		return fmt.Errorf("%w: group contains %d terminals; move them or explicitly remove the group with force", ErrGroupConflict, len(ids))
	}
	for _, id := range ids {
		if err := m.closeTerminal(id); err != nil && !errors.Is(err, ErrNotFound) {
			return err
		}
	}
	next := cloneGroups(m.groups)
	delete(next, g.ID)
	if err := m.persistGroups(next); err != nil {
		return err
	}
	m.groups = next
	return nil
}

// UpdateTerminal changes identity metadata without replacing its process.
func (m *Manager) UpdateTerminal(id ID, update TerminalUpdate) (Info, error) {
	m.workspaceMu.Lock()
	defer m.workspaceMu.Unlock()
	current, err := m.Get(id)
	if err != nil {
		return Info{}, err
	}
	if update.Name != nil {
		name, err := cleanGroupName(*update.Name)
		if err != nil {
			return Info{}, err
		}
		current.Name = name
	}
	if update.GroupID != nil {
		g, err := m.groupLocked(*update.GroupID)
		if err != nil {
			return Info{}, err
		}
		if g.Archived {
			return Info{}, fmt.Errorf("%w: unarchive the destination group first", ErrGroupConflict)
		}
		current.GroupID = g.ID
	}
	if m.opts.PersistDir != "" {
		meta, err := readMeta(terminalDir(m.opts.PersistDir, id))
		if err != nil {
			return Info{}, err
		}
		meta.Name, meta.GroupID = current.Name, current.GroupID
		if err := writeMeta(m.opts.PersistDir, meta); err != nil {
			return Info{}, err
		}
	}
	m.mu.Lock()
	if t := m.terms[id]; t != nil {
		t.mu.Lock()
		t.info.Name, t.info.GroupID = current.Name, current.GroupID

		t.mu.Unlock()
		m.mu.Unlock()
		if current.Source.Provider != "" && update.GroupID != nil {
			m.expireNativeView(id)
		}
		return current, nil
	}
	if _, ok := m.ghosts[id]; ok {
		m.ghosts[id] = current
		m.mu.Unlock()
		return current, nil
	}
	m.mu.Unlock()
	return Info{}, ErrNotFound
}
func (m *Manager) SetAddress(address string) {
	m.workspaceMu.Lock()
	defer m.workspaceMu.Unlock()
	m.address = address
}
