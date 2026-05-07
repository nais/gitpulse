package cache

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

type Entry struct {
	Data      json.RawMessage `json:"data"`
	FetchedAt time.Time       `json:"fetched_at"`
}

func (e *Entry) Age() time.Duration {
	return time.Since(e.FetchedAt)
}

func (e *Entry) IsStale(ttl time.Duration) bool {
	return e.Age() > ttl
}

func cacheDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".cache", "gitpulse")
	return dir, os.MkdirAll(dir, 0755)
}

func cachePath() (string, error) {
	dir, err := cacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "dashboard.json"), nil
}

func Load() (*Entry, error) {
	path, err := cachePath()
	if err != nil {
		return nil, err
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var entry Entry
	if err := json.Unmarshal(data, &entry); err != nil {
		return nil, err
	}
	return &entry, nil
}

func Save(data json.RawMessage) error {
	path, err := cachePath()
	if err != nil {
		return err
	}

	entry := Entry{
		Data:      data,
		FetchedAt: time.Now(),
	}

	raw, err := json.Marshal(entry)
	if err != nil {
		return err
	}

	return os.WriteFile(path, raw, 0644)
}
