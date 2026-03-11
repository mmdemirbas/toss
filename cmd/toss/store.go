package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

type Config struct {
	DeviceID   string `json:"deviceId"`
	DeviceName string `json:"deviceName"`
}

type Store struct {
	mu       sync.RWMutex
	dir      string
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
		filesDir: filesDir,
	}

	s.loadConfig()
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

func (s *Store) FilePath(fileID string) string {
	return filepath.Join(s.filesDir, filepath.Base(fileID))
}
