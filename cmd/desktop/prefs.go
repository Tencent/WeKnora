package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Tencent/WeKnora/internal/localsandbox"
)

const desktopPrefsFileName = "desktop-prefs.json"

type desktopPrefs struct {
	HTTPPort       int      `json:"http_port"`
	HTTPBindPublic bool     `json:"http_bind_public"`
	ProjectDirs    []string `json:"project_dirs,omitempty"`
	ApprovalMode   string   `json:"approval_mode,omitempty"`
	SessionRoot    string   `json:"session_root,omitempty"`
}

// Which stances exist, and which of them this build can serve, are decided by
// the sandbox itself (localsandbox.ApprovalMode). Keeping a second list here
// is how the two drift.

func desktopConfigDir() (string, error) {
	// Tests (and some Unix setups) isolate via XDG_CONFIG_HOME. Darwin's
	// UserConfigDir ignores that variable, so honor it first or prefs tests
	// write into the real Application Support directory.
	if xdg := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME")); xdg != "" {
		return xdg, nil
	}
	return os.UserConfigDir()
}

func desktopPrefsDir() (string, error) {
	cfg, err := desktopConfigDir()
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

// LoadApprovalMode returns the stored mode. Unknown values become "auto".
// Known-but-unshipped modes are returned unchanged so the sandbox can refuse
// them instead of silently widening access.
func LoadApprovalMode() string {
	return string(localsandbox.ParseApprovalMode(loadDesktopPrefs().ApprovalMode))
}

// SaveApprovalMode refuses what it cannot deliver. Storing "ask" would promise
// an approval card that does not exist yet, and "full" would promise an
// unsandboxed run that the sandbox has no policy for; both look like a working
// setting and behave like a broken one.
func SaveApprovalMode(mode string) error {
	requested := localsandbox.ApprovalMode(mode)
	if !requested.Known() {
		return fmt.Errorf("invalid approval mode %q", mode)
	}
	if !requested.Shipped() {
		return fmt.Errorf("approval mode %q is not available yet", mode)
	}
	cur := loadDesktopPrefs()
	cur.ApprovalMode = mode
	return saveDesktopPrefs(cur)
}

// LoadProjectDirs returns the project directories the user picked. Only the UI
// writes this list: the agent must never widen its own reach.
func LoadProjectDirs() []string {
	return loadDesktopPrefs().ProjectDirs
}

func SaveProjectDirs(dirs []string) error {
	cleaned := make([]string, 0, len(dirs))
	for _, dir := range dirs {
		if !filepath.IsAbs(dir) {
			return fmt.Errorf("project directory must be absolute: %q", dir)
		}
		cleaned = append(cleaned, filepath.Clean(dir))
	}
	cur := loadDesktopPrefs()
	cur.ProjectDirs = cleaned
	return saveDesktopPrefs(cur)
}

// appendApprovedProjectDir adds an absolute directory the user just picked.
// Duplicate paths are ignored so opening the same folder twice does not
// grow the recent list.
func appendApprovedProjectDir(dir string) (string, error) {
	dir = filepath.Clean(strings.TrimSpace(dir))
	if dir == "" || dir == "." {
		return "", nil
	}
	existing := LoadProjectDirs()
	for _, item := range existing {
		if item == dir {
			return dir, nil
		}
	}
	if err := SaveProjectDirs(append(append([]string{}, existing...), dir)); err != nil {
		return "", err
	}
	return dir, nil
}

// removeApprovedProjectDir drops one previously picked directory. Missing
// paths are a no-op so clearing the same folder twice does not error.
func removeApprovedProjectDir(dir string) error {
	dir = filepath.Clean(strings.TrimSpace(dir))
	if dir == "" || dir == "." {
		return nil
	}
	existing := LoadProjectDirs()
	kept := make([]string, 0, len(existing))
	for _, item := range existing {
		if item != dir {
			kept = append(kept, item)
		}
	}
	return SaveProjectDirs(kept)
}
