package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

const desktopPrefsFileName = "desktop-prefs.json"

type desktopPrefs struct {
	HTTPPort int `json:"http_port"`
}

func desktopPrefsDir() (string, error) {
	cfg, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(cfg, "WeKnora Lite")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return dir, nil
}

func desktopPrefsFilePath() (string, error) {
	dir, err := desktopPrefsDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, desktopPrefsFileName), nil
}

// LoadDesktopPrefsHTTPPort returns http_port from prefs file, or 0 if unset / invalid (ephemeral port on each launch).
func LoadDesktopPrefsHTTPPort() int {
	path, err := desktopPrefsFilePath()
	if err != nil {
		return 0
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	var p desktopPrefs
	if json.Unmarshal(data, &p) != nil {
		return 0
	}
	if p.HTTPPort < 0 || p.HTTPPort > 65535 {
		return 0
	}
	return p.HTTPPort
}

// SaveDesktopHTTPPortPreference persists listen port preference. port 0 means use a random free port on each launch.
func SaveDesktopHTTPPortPreference(port int) error {
	if port < 0 || port > 65535 {
		return fmt.Errorf("invalid port")
	}
	path, err := desktopPrefsFilePath()
	if err != nil {
		return err
	}
	p := desktopPrefs{HTTPPort: port}
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}
