package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/naoto/airplayer-go/airplay"
)

// credentialStore persists pairing credentials keyed by device ID.
type credentialStore struct {
	path    string
	entries map[string]*airplay.Credentials
}

func loadCredentials() (*credentialStore, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return nil, err
	}
	s := &credentialStore{
		path:    filepath.Join(dir, "airplayer", "credentials.json"),
		entries: map[string]*airplay.Credentials{},
	}
	b, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, &s.entries); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *credentialStore) get(d airplay.Device) *airplay.Credentials {
	return s.entries[d.ID()]
}

func (s *credentialStore) set(d airplay.Device, c *airplay.Credentials) error {
	s.entries[d.ID()] = c
	b, err := json.MarshalIndent(s.entries, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(s.path, b, 0o600)
}
