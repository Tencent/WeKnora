package sandbox

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// DetectLocalDockerHost returns the daemon endpoint the Docker CLI would use
// on this machine when the workspace config leaves the host blank: DOCKER_HOST
// first, then the current docker context, then DefaultDockerHost.
//
// /var/run/docker.sock is the Linux default and is often absent on macOS
// (Colima, Docker Desktop, OrbStack each expose a socket under $HOME). An
// operator who can run `docker ps` should not have to copy that path into
// the settings form.
func DetectLocalDockerHost() string {
	if host := strings.TrimSpace(os.Getenv("DOCKER_HOST")); host != "" {
		return host
	}
	if host := dockerCLIContextHost(); host != "" {
		return host
	}
	return DefaultDockerHost
}

func dockerCLIContextHost() string {
	configDir := strings.TrimSpace(os.Getenv("DOCKER_CONFIG"))
	if configDir == "" {
		home, err := os.UserHomeDir()
		if err != nil || home == "" {
			return ""
		}
		configDir = filepath.Join(home, ".docker")
	}

	name := strings.TrimSpace(os.Getenv("DOCKER_CONTEXT"))
	if name == "" {
		name = dockerCurrentContextName(configDir)
	}
	if name == "" || name == "default" {
		return ""
	}

	entries, err := os.ReadDir(filepath.Join(configDir, "contexts", "meta"))
	if err != nil {
		return ""
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		host := dockerContextHost(
			filepath.Join(configDir, "contexts", "meta", entry.Name(), "meta.json"),
			name,
		)
		if host != "" {
			return host
		}
	}
	return ""
}

func dockerCurrentContextName(configDir string) string {
	body, err := os.ReadFile(filepath.Join(configDir, "config.json"))
	if err != nil {
		return ""
	}
	var parsed struct {
		CurrentContext string `json:"currentContext"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return ""
	}
	return strings.TrimSpace(parsed.CurrentContext)
}

func dockerContextHost(metaPath, wantName string) string {
	body, err := os.ReadFile(metaPath)
	if err != nil {
		return ""
	}
	var parsed struct {
		Name      string `json:"Name"`
		Endpoints map[string]struct {
			Host string `json:"Host"`
		} `json:"Endpoints"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return ""
	}
	if parsed.Name != wantName {
		return ""
	}
	if parsed.Endpoints == nil {
		return ""
	}
	return strings.TrimSpace(parsed.Endpoints["docker"].Host)
}
