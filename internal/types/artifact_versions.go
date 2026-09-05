package types

import (
	"strings"
)

// Do not silently redirect old handles: the user may be comparing versions.
// When an answer references a previous version of a regenerated output, attach
// an explicit old/current pair. Preserve all original prose and destinations.
func ClarifyArtifactVersions(content string, current, previous MessageArtifacts, language string) string {
	oldLabel, newLabel := "Referenced previous version", "File generated this turn"
	if strings.HasPrefix(language, "zh") {
		oldLabel, newLabel = "正文引用的历史版本", "本轮生成的文件"
	}
	seen := make(map[string]bool)
	for _, old := range previous {
		for _, next := range current {
			if old.SourcePath == "" || old.SourcePath != next.SourcePath || old.URL == next.URL || seen[next.URL] || strings.Contains(content, next.URL) {
				continue
			}
			if _, ok := ParseResourcePath(next.URL); !ok {
				continue
			}
			seen[next.URL] = true
			name := strings.NewReplacer("\\", "\\\\", "[", "\\[", "]", "\\]", "(", "\\(", ")", "\\)", "\n", " ", "\r", " ").Replace(next.FileName)
			content += "\n\n" + oldLabel + ": ![" + name + "](" + old.URL + ")\n\n" +
				newLabel + ": ![" + name + "](" + next.URL + ")"
		}
	}
	return content
}
