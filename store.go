package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

type Config struct {
	DeviceID   string `json:"deviceId"`
	DeviceName string `json:"deviceName"`
	Token      string `json:"token"`
	SavedToken string `json:"savedToken"`
	AuthMode   string `json:"authMode"`
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
	dir := filepath.Join(home, ".lanpane")
	filesDir := filepath.Join(dir, "files")
	os.MkdirAll(filesDir, 0755)

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
			AuthMode:   "optional",
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
	mode := strings.ToLower(strings.TrimSpace(s.config.AuthMode))
	if mode != "required" && mode != "optional" {
		s.config.AuthMode = "optional"
		return
	}
	s.config.AuthMode = mode
}

func (s *Store) saveConfig() {
	data, _ := json.MarshalIndent(s.config, "", "  ")
	os.WriteFile(filepath.Join(s.dir, "config.json"), data, 0644)
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

func (s *Store) SetToken(token string) {
	s.config.Token = token
	s.saveConfig()
}

func (s *Store) SetSavedToken(token string) {
	s.config.SavedToken = token
	s.saveConfig()
}

func (s *Store) SetAuthMode(mode string) {
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode != "required" && mode != "optional" {
		return
	}
	s.config.AuthMode = mode
	s.saveConfig()
}

func (s *Store) FilePath(fileID string) string {
	return filepath.Join(s.filesDir, fileID)
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
