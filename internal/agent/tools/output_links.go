package tools

import (
	"context"
	"net/url"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/agent/skills"
	"github.com/Tencent/WeKnora/internal/sandbox"
)

// Metadata snapshots detect outputs without parsing arbitrary commands or stdout.
// Inspection is best-effort and never provisions a sandbox or downloads files.
func sandboxOutputSnapshot(ctx context.Context, executor SandboxCommandExecutor, sessionID string) (map[string]sandbox.RemoteDirEntry, bool) {
	source, ok := executor.(interface {
		ListSessionFiles(context.Context, string, string) ([]sandbox.RemoteDirEntry, error)
	})
	if !ok {
		return nil, false
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	entries, err := source.ListSessionFiles(ctx, sessionID, skills.ArtifactOutputDir())
	if err != nil {
		return nil, false
	}
	files := make(map[string]sandbox.RemoteDirEntry, len(entries))
	for _, entry := range entries {
		if entry.Type == sandbox.RemoteEntryFile {
			files[entry.Path] = entry
		}
	}
	return files, true
}

func changedOutputLinks(before, after map[string]sandbox.RemoteDirEntry) []string {
	var paths []string
	for filePath, next := range after {
		old, exists := before[filePath]
		if !exists || old.Size != next.Size || !old.ModTime.Equal(next.ModTime) {
			paths = append(paths, filePath)
		}
	}
	sort.Strings(paths)
	return sandboxOutputLinks(paths...)
}

func sandboxOutputLinks(paths ...string) []string {
	var links []string
	bytes := 0
	for _, filePath := range paths {
		filePath = path.Clean(filePath)
		if !strings.HasPrefix(filePath, path.Clean(skills.ArtifactOutputDir())+"/") {
			continue
		}
		name := path.Base(filePath)
		// Keep Unicode readable; escape only ASCII Markdown/URL delimiters.
		var escaped strings.Builder
		for _, char := range name {
			if char >= 128 {
				escaped.WriteRune(char)
			} else {
				escaped.WriteString(url.PathEscape(string(char)))
			}
		}
		link := "sandbox:" + escaped.String()
		if bytes+len(link) > 8*1024 {
			break
		}
		links = append(links, link)
		bytes += len(link)
	}
	return links
}
