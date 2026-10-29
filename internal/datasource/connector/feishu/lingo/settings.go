package lingo

import (
	"fmt"
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
)

// defaultFolderPath is the knowledge base folder Lingo terms are placed under.
const defaultFolderPath = "飞书词典"

// settings holds the non-secret configuration carried in DataSourceConfig.Settings.
type settings struct {
	FolderPath string
}

func parseSettings(config *types.DataSourceConfig) (settings, error) {
	folder := defaultFolderPath
	if config != nil && config.Settings != nil {
		if raw, ok := config.Settings["folder_path"].(string); ok && strings.TrimSpace(raw) != "" {
			folder = strings.Trim(strings.ReplaceAll(strings.TrimSpace(raw), "\\", "/"), "/")
		}
	}
	for _, part := range strings.Split(folder, "/") {
		if part == "" || part == "." || part == ".." {
			return settings{}, fmt.Errorf("invalid folder_path %q", folder)
		}
	}
	return settings{FolderPath: folder}, nil
}

// buildFolderFileName mirrors how other connectors place a document in a
// knowledge base folder: the relative path is encoded in FileName and the
// ingest pipeline derives folder_path from it.
func buildFolderFileName(folder, name string) string {
	if strings.TrimSpace(folder) == "" {
		return name
	}
	return folder + "/" + name
}
