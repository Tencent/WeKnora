package compat

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"
)

const ttl = 24 * time.Hour

// cachePath returns $XDG_CACHE_HOME/weknora/server-info.yaml,fallback ~/.cache/weknora/.
func cachePath() (string, error) {
	if x := os.Getenv("XDG_CACHE_HOME"); x != "" {
		return filepath.Join(x, "weknora", "server-info.yaml"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("locate home dir: %w", err)
	}
	return filepath.Join(home, ".cache", "weknora", "server-info.yaml"), nil
}

// LoadCache reads the cached Info. Returns (info, fresh, err).
//
//	info == nil when no cache exists (err == nil)
//	fresh == false 当 cache 不存在 / TTL 过期
func LoadCache() (*Info, bool, error) {
	p, err := cachePath()
	if err != nil {
		return nil, false, err
	}
	data, err := os.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("read cache: %w", err)
	}
	var info Info
	if err := yaml.Unmarshal(data, &info); err != nil {
		return nil, false, fmt.Errorf("parse cache: %w", err)
	}
	fresh := time.Since(info.ProbedAt) < ttl
	return &info, fresh, nil
}

// SaveCache atomically writes Info to the cache file (mode 0600).
func SaveCache(info *Info) error {
	p, err := cachePath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		return fmt.Errorf("mkdir cache dir: %w", err)
	}
	data, err := yaml.Marshal(info)
	if err != nil {
		return fmt.Errorf("marshal cache: %w", err)
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return fmt.Errorf("write tmp cache: %w", err)
	}
	if err := os.Rename(tmp, p); err != nil {
		return fmt.Errorf("rename cache: %w", err)
	}
	return nil
}
