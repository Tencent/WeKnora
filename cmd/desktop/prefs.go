package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

const desktopPrefsFileName = "desktop-prefs.json"

type desktopPrefs struct {
	HTTPPort       int  `json:"http_port"`
	HTTPBindPublic bool `json:"http_bind_public"`
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

func loadDesktopPrefs() desktopPrefs {
	path, err := desktopPrefsFilePath()
	if err != nil {
		return desktopPrefs{}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return desktopPrefs{}
	}
	var p desktopPrefs
	if json.Unmarshal(data, &p) != nil {
		return desktopPrefs{}
	}
	if p.HTTPPort < 0 || p.HTTPPort > 65535 {
		p.HTTPPort = 0
	}
	return p
}

func saveDesktopPrefs(p desktopPrefs) error {
	path, err := desktopPrefsFilePath()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

// LoadDesktopPrefsHTTPPort returns http_port from prefs file, or 0 if unset / invalid (ephemeral port on each launch).
func LoadDesktopPrefsHTTPPort() int {
	return loadDesktopPrefs().HTTPPort
}

// LoadDesktopHTTPBindPublic returns whether the embedded API server should listen on all interfaces (0.0.0.0).
func LoadDesktopHTTPBindPublic() bool {
	return loadDesktopPrefs().HTTPBindPublic
}

// SaveDesktopHTTPPortPreference persists listen port preference. port 0 means use a random free port on each launch.
func SaveDesktopHTTPPortPreference(port int) error {
	if port < 0 || port > 65535 {
		return fmt.Errorf("invalid port")
	}
	cur := loadDesktopPrefs()
	cur.HTTPPort = port
	return saveDesktopPrefs(cur)
}

// SaveDesktopHTTPBindPublicPreference persists whether to listen on 0.0.0.0 for LAN/public access.
func SaveDesktopHTTPBindPublicPreference(v bool) error {
	cur := loadDesktopPrefs()
	cur.HTTPBindPublic = v
	return saveDesktopPrefs(cur)
}
