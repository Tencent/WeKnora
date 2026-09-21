package tools

import (
	"context"
	"net/url"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/sandbox"
)

// Metadata snapshots detect outputs without parsing arbitrary commands or stdout.
// Inspection is best-effort and never provisions a sandbox or downloads files.
//
// outputDir comes from the layout the caller already resolved for this
// command, so the before/after probes cannot describe a different tree than
// the one the links are filtered against.
func sandboxOutputSnapshot(
	ctx context.Context, executor SandboxCommandExecutor, sessionID, outputDir string,
) (map[string]sandbox.RemoteDirEntry, bool) {
	if strings.TrimSpace(outputDir) == "" {
		return nil, false
	}
	source, ok := executor.(interface {
		ListSessionFiles(context.Context, string, string) ([]sandbox.RemoteDirEntry, error)
	})
	if !ok {
		return nil, false
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	entries, err := source.ListSessionFiles(ctx, sessionID, outputDir)
	if err != nil {
		return nil, false
	}
	return outputEntriesSnapshot(entries), true
}

func outputEntriesSnapshot(entries []sandbox.RemoteDirEntry) map[string]sandbox.RemoteDirEntry {
	files := make(map[string]sandbox.RemoteDirEntry, len(entries))
	for _, entry := range entries {
		if entry.Type == sandbox.RemoteEntryFile {
			files[entry.Path] = entry
		}
	}
	return files
}

func changedOutputLinks(outputDir string, before, after map[string]sandbox.RemoteDirEntry) []string {
	var paths []string
	for filePath, next := range after {
		old, exists := before[filePath]
		if !exists || old.Size != next.Size || !old.ModTime.Equal(next.ModTime) {
			paths = append(paths, filePath)
		}
	}
	sort.Strings(paths)
	return sandboxOutputLinksIn(outputDir, paths...)
}

func sandboxOutputLinksIn(outputDir string, paths ...string) []string {
	// A non-nil empty list means inspection completed without finding output
	// links. Keep it distinct from nil (inspection unavailable or failed) so the
	// next model turn can distinguish stdout filenames from deliverables.
	links := []string{}
	bytes := 0
	if outputDir == "" {
		return links
	}
	outputDir = path.Clean(outputDir)
	for _, filePath := range paths {
		filePath = path.Clean(filePath)
		if !strings.HasPrefix(filePath, outputDir+"/") {
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
