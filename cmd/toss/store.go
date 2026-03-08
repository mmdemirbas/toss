package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sync"
)

type Config struct {
	DeviceID   string          `json:"deviceId"`
	DeviceName string          `json:"deviceName"`
	Clipboard  ClipboardConfig `json:"clipboard"`
}

type Store struct {
	mu       sync.RWMutex
	dir      string
	panes    map[string]*Pane
	config   Config
	filesDir string
}

func NewStore() (*Store, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(home, ".toss")
	filesDir := filepath.Join(dir, "files")
	if err := os.MkdirAll(filesDir, 0755); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}

	s := &Store{
		dir:      dir,
		panes:    make(map[string]*Pane),
		filesDir: filesDir,
	}

	s.loadConfig()
	s.loadPanes()
	return s, nil
}

func (s *Store) loadConfig() {
	data, err := os.ReadFile(filepath.Join(s.dir, "config.json"))
	if err != nil {
		hostname, _ := os.Hostname()
		if hostname == "" {
			hostname = "device-" + generateID()[:4]
		}
		s.config = Config{
			DeviceID:   generateID(),
			DeviceName: hostname,
		}
		s.saveConfig()
		return
	}
	json.Unmarshal(data, &s.config)
	s.normalizeConfig()
	s.saveConfig()
}

func (s *Store) normalizeConfig() {
	if s.config.DeviceID == "" {
		s.config.DeviceID = generateID()
	}
	if s.config.DeviceName == "" {
		hostname, _ := os.Hostname()
		if hostname == "" {
			hostname = "device-" + generateID()[:4]
		}
		s.config.DeviceName = hostname
	}
}

func (s *Store) saveConfig() {
	data, _ := json.MarshalIndent(s.config, "", "  ")
	os.WriteFile(filepath.Join(s.dir, "config.json"), data, 0600)
}

func (s *Store) loadPanes() {
	data, err := os.ReadFile(filepath.Join(s.dir, "panes.json"))
	if err != nil {
		return
	}
	var panes []Pane
	if err := json.Unmarshal(data, &panes); err != nil {
		return
	}
	for i := range panes {
		s.panes[panes[i].ID] = &panes[i]
	}
}

func (s *Store) savePanes() {
	s.mu.RLock()
	panes := make([]Pane, 0, len(s.panes))
	for _, p := range s.panes {
		panes = append(panes, *p)
	}
	s.mu.RUnlock()
	data, _ := json.MarshalIndent(panes, "", "  ")
	os.WriteFile(filepath.Join(s.dir, "panes.json"), data, 0644)
}

func (s *Store) GetPanes() []Pane {
	s.mu.RLock()
	defer s.mu.RUnlock()
	panes := make([]Pane, 0, len(s.panes))
	for _, p := range s.panes {
		panes = append(panes, *p)
	}
	return panes
}

func (s *Store) GetPane(id string) *Pane {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if p, ok := s.panes[id]; ok {
		cp := *p
		return &cp
	}
	return nil
}

func (s *Store) UpsertPane(p Pane) {
	s.mu.Lock()
	existing, ok := s.panes[p.ID]
	if ok && existing.Version >= p.Version {
		s.mu.Unlock()
		return
	}
	s.panes[p.ID] = &p
	s.mu.Unlock()
	s.savePanes()
}

func (s *Store) DeletePane(id string) {
	s.mu.Lock()
	delete(s.panes, id)
	s.mu.Unlock()
	s.savePanes()
}

// DeletePaneWithFiles deletes a pane and removes any files referenced in its content.
func (s *Store) DeletePaneWithFiles(id string) {
	pane := s.GetPane(id)
	s.DeletePane(id)
	if pane != nil {
		s.deleteReferencedFiles(pane.Content)
	}
}

var fileRefRe = regexp.MustCompile(`/api/files/([^\s)"'\]]+)`)

func (s *Store) deleteReferencedFiles(content string) {
	matches := fileRefRe.FindAllStringSubmatch(content, -1)
	for _, m := range matches {
		fileID := filepath.Base(m[1])
		if fileID == "" || fileID == "." || fileID == ".." {
			continue
		}
		path := s.FilePath(fileID)
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			log.Printf("[files] failed to delete %s: %v", fileID, err)
		} else if err == nil {
			log.Printf("[files] deleted %s (pane removed)", fileID)
		}
	}
}

func (s *Store) FilePath(fileID string) string {
	return filepath.Join(s.filesDir, filepath.Base(fileID))
}

func (s *Store) ReplacePanes(panes []Pane) {
	s.mu.Lock()
	s.panes = make(map[string]*Pane)
	for i := range panes {
		s.panes[panes[i].ID] = &panes[i]
	}
	s.mu.Unlock()
	s.savePanes()
}

func (s *Store) GetClipboardConfig() ClipboardConfig {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.config.Clipboard
}

func (s *Store) SetClipboardConfig(cfg ClipboardConfig) {
	s.mu.Lock()
	s.config.Clipboard = cfg
	s.mu.Unlock()
	s.saveConfig()
}
