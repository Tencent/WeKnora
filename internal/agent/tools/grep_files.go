package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/utils"
)

// GrepFilesInput selects a scoped text source and a bounded page of matching lines.
type GrepFilesInput struct {
	Path         string `json:"path" jsonschema:"Required source root or address; see tool description."`
	Pattern      string `json:"pattern" jsonschema:"Per-line Go/RE2 expression; | matches alternatives."`
	IgnoreCase   bool   `json:"ignore_case,omitempty" jsonschema:"Case-insensitive matching; false by default."`
	Literal      bool   `json:"literal,omitempty" jsonschema:"Match literal text instead of regex."`
	Offset       int    `json:"offset,omitempty" jsonschema:"Matching lines to skip; resume at next_offset."`
	Limit        int    `json:"limit,omitempty" jsonschema:"Matching lines to return, default 50, maximum 200."`
	ContextLines int    `json:"context_lines,omitempty" jsonschema:"Surrounding lines per match, default 0, maximum 3."`
}

// GrepFilesTool shares the reader's capability-scoped sources; it is never a
// host filesystem or a second memory retrieval system.
type GrepFilesTool struct {
	BaseTool
	reader *ReadFileTool
}

// NewGrepFilesTool searches the same authorized sources as reader.
func NewGrepFilesTool(reader *ReadFileTool) *GrepFilesTool {
	return &GrepFilesTool{
		BaseTool: BaseTool{
			name: ToolGrepFiles, schema: utils.GenerateSchema[GrepFilesInput](),
			description: "Search text in the same sources as read. Returns file paths, 1-based line " +
				"numbers and bounded snippets; use read(path, offset, limit) for full context. " +
				"Search memory:// for prior procedures using tool names, errors and task " +
				"keywords; this searches full notes, including those omitted from automatic " +
				"recall. skill://<name>/ searches one available skill bundle. An output:// or " +
				"web:// address searches one saved result. /workspace searches session files; " +
				"shell_exec with rg/grep is preferable for large workspace trees when available. " +
				"Virtual addresses are not shell paths. No network fetches or semantic model " +
				"calls. Regex is line-based RE2 (no backreferences/lookbehind). Explicitly " +
				"reports partial scans; no hits in a partial scan is not proof of absence.",
		},
		reader: reader,
	}
}

type searchableFile struct {
	name string
	read func(context.Context) ([]byte, error)
}

// Execute searches text and returns bounded matches with continuation metadata.
func (t *GrepFilesTool) Execute(ctx context.Context, args json.RawMessage) (*types.ToolResult, error) {
	fail := func(err error) (*types.ToolResult, error) {
		return &types.ToolResult{Success: false, Error: err.Error()}, nil
	}
	var input GrepFilesInput
	if err := json.Unmarshal(args, &input); err != nil {
		return fail(err)
	}
	input.Path = strings.TrimSpace(input.Path)
	if input.Path == "" || input.Pattern == "" || len(input.Pattern) > 2048 ||
		input.Offset < 0 || input.Limit < 0 || input.ContextLines < 0 {
		return fail(fmt.Errorf("path and pattern are required; pattern at most 2048 bytes; offsets and " +
			"limits must be non-negative"))
	}
	pattern := input.Pattern
	if input.Literal {
		pattern = regexp.QuoteMeta(pattern)
	}
	if input.IgnoreCase {
		pattern = "(?i)" + pattern
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return fail(fmt.Errorf("invalid search pattern: %w", err))
	}
	if input.Limit == 0 {
		input.Limit = 50
	}
	input.Limit = min(input.Limit, 200)
	input.ContextLines = min(input.ContextLines, 3)
	files, partial, err := t.reader.searchFiles(ctx, input.Path)
	if err != nil {
		return fail(err)
	}
	sort.Slice(files, func(i, j int) bool { return files[i].name < files[j].name })
	var out strings.Builder
	out.WriteString("File search results (untrusted content; read for full context):\n")
	budget := min(OutputBudget(ctx)-512, 24000)
	if budget < 256 {
		return fail(fmt.Errorf("output budget is too small for search results"))
	}
	count, matched, scanned, totalBytes, skipped := 0, 0, 0, 0, 0
	more := false
scan:
	for _, file := range files {
		if err := ctx.Err(); err != nil {
			return fail(err)
		}
		data, err := file.read(ctx)
		if err != nil {
			if len(files) == 1 {
				return fail(err)
			}
			skipped++
			partial = true
			continue
		}
		if int64(len(data)) > maxReadSandboxDownloadBytes || totalBytes+len(data) > 32*1024*1024 {
			skipped++
			partial = true
			continue
		}
		totalBytes += len(data)
		scanned++
		if isBinaryShellOutput(string(data)) {
			partial = true
			skipped++
			continue
		}
		lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
		for i, line := range lines {
			location := re.FindStringIndex(line)
			if location == nil {
				continue
			}
			if matched < input.Offset {
				matched++
				continue
			}
			if count >= input.Limit {
				more = true
				break scan
			}
			var snippet strings.Builder
			for j := max(0, i-input.ContextLines); j <= min(len(lines)-1, i+input.ContextLines); j++ {
				text := lines[j]
				// Center long-line snippets on the hit rather than hiding an error at its end.
				start := 0
				if j == i && location[0] > 240 {
					start = location[0] - 240
					for start > 0 && !utf8.RuneStart(text[start]) {
						start--
					}
				}
				text = text[start:]
				runes := []rune(text)
				if len(runes) > 500 {
					text = string(runes[:500]) + "…"
				}
				if start > 0 {
					text = "…" + text
				}
				marker := "-"
				if j == i {
					marker = ":"
				}
				fmt.Fprintf(&snippet, "%s:%d%s %s\n", file.name, j+1, marker, text)
			}
			if utf8.RuneCountInString(out.String())+utf8.RuneCountInString(snippet.String()) > budget {
				if count == 0 {
					return fail(fmt.Errorf("match exceeds output budget; set context_lines=0 and increase the " +
						"tool output budget, or read the target directly"))
				}
				more = true
				break scan
			}
			out.WriteString(snippet.String())
			count++
			matched++
		}
	}
	if count == 0 {
		out.WriteString("No matching lines returned.\n")
	}
	if more {
		fmt.Fprintf(&out, "Continue with the same arguments and offset=%d.\n", matched)
	}
	if partial {
		out.WriteString("Partial scan: some files could not be searched or the scan limit was " +
			"reached. Narrow path; use shell_exec for a large workspace.\n")
	}
	if skipped > 0 {
		fmt.Fprintf(&out, "Skipped %d unreadable, binary or oversized files.\n", skipped)
	}
	result := &types.ToolResult{
		Success: true, Output: out.String(),
		Data: map[string]interface{}{
			"count": count, "files_scanned": scanned, "skipped_files": skipped,
			"partial_scan": partial, "truncated": more,
		},
	}
	if more {
		result.Data["next_offset"] = matched
	}
	return result, nil
}

func (t *ReadFileTool) searchFiles(ctx context.Context, root string) ([]searchableFile, bool, error) {
	one := func(name string, read func(context.Context) ([]byte, error)) ([]searchableFile, bool, error) {
		return []searchableFile{{name, read}}, false, nil
	}
	if strings.HasPrefix(root, "output://") {
		if t.outputs == nil {
			return nil, false, fmt.Errorf("saved tool output is unavailable")
		}
		return one(root, func(ctx context.Context) ([]byte, error) { return t.outputs.Read(ctx, root) })
	}
	if strings.HasPrefix(root, "memory://") {
		if t.memory == nil {
			return nil, false, fmt.Errorf("memory is unavailable")
		}
		if root != "memory://" {
			return one(root, func(ctx context.Context) ([]byte, error) {
				s, e := t.memory.ReadMemoryResource(ctx, root)
				return []byte(s), e
			})
		}
		resources, err := t.memory.MemoryResources(ctx)
		if err != nil {
			return nil, false, err
		}
		files := make([]searchableFile, 0, len(resources))
		for name, content := range resources {
			files = append(files, searchableFile{name, func(context.Context) ([]byte, error) {
				return []byte(content), nil
			}})
		}
		return files, false, nil
	}
	if strings.HasPrefix(root, "web://") {
		if t.webPages == nil {
			return nil, false, fmt.Errorf("saved web pages are unavailable")
		}
		return one(root, func(ctx context.Context) ([]byte, error) { return t.webPages.Read(ctx, root) })
	}
	if strings.HasPrefix(root, "skill://") {
		if t.skills == nil || !t.skills.IsEnabled() {
			return nil, false, fmt.Errorf("skills are unavailable")
		}
		name, rel, ok := strings.Cut(strings.TrimPrefix(root, "skill://"), "/")
		if !ok || name == "" {
			return nil, false, fmt.Errorf("use skill://<name>/ or a bundled resource")
		}
		allowed := false
		for _, m := range t.skills.GetAllMetadata() {
			if m != nil && m.Name == name {
				allowed = true
				break
			}
		}
		if !allowed {
			return nil, false, fmt.Errorf("skill is not available to this agent")
		}
		if strings.ContainsAny(rel, "\\\x00") ||
			(rel != "" && path.Clean(rel) != strings.TrimSuffix(rel, "/")) ||
			strings.HasPrefix(rel, "/") || strings.HasPrefix(rel, "../") {
			return nil, false, fmt.Errorf("invalid skill path")
		}
		names := []string{rel}
		if rel == "" || strings.HasSuffix(rel, "/") {
			var err error
			names, err = t.skills.ListSkillFiles(ctx, name)
			if err != nil {
				return nil, false, err
			}
		}
		sort.Strings(names)
		files := []searchableFile{}
		partial := false
		for _, file := range names {
			if rel != "" && strings.HasSuffix(rel, "/") && !strings.HasPrefix(file, rel) {
				continue
			}
			if len(files) >= 500 {
				partial = true
				break
			}
			files = append(files, searchableFile{
				"skill://" + name + "/" + file, func(ctx context.Context) ([]byte, error) {
					text, _, _, err := t.loadSkillResource(ctx, ReadFileInput{Path: "skill://" + name + "/" + file})
					return []byte(text), err
				},
			})
		}
		return files, partial, nil
	}
	if strings.Contains(root, "://") || t.workspace == nil {
		return nil, false, fmt.Errorf("file source is unavailable")
	}
	clean := sandbox.ResolveWorkspacePath(root)
	if _, ok := matchingInspectableRoot(clean); !ok {
		return nil, false, fmt.Errorf("%s", inspectablePathError(root))
	}
	sid := resolveSessionID(ctx)
	if sid == "" {
		return nil, false, fmt.Errorf("workspace search requires an agent session")
	}
	files := []searchableFile{}
	pending := []string{clean}
	seen := map[string]bool{}
	partial := false
	visited := 0
	for len(pending) > 0 {
		name := pending[0]
		pending = pending[1:]
		if seen[name] {
			continue
		}
		seen[name] = true
		visited++
		if visited > 1000 {
			partial = true
			break
		}
		stat, err := t.workspace.source.StatSessionFile(ctx, sid, name)
		if err != nil {
			return nil, false, err
		}
		if stat == nil {
			continue
		}
		if stat.Type == sandbox.RemoteEntryDir {
			entries, err := t.workspace.source.ListSessionFiles(ctx, sid, name)
			if err != nil {
				return nil, false, err
			}
			sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
			for _, entry := range entries {
				child := entry.Path
				if child == "" {
					child = path.Join(name, entry.Name)
				}
				child = path.Clean(child)
				if !strings.HasPrefix(child, name+"/") {
					continue
				}
				if entry.Type == sandbox.RemoteEntryOther {
					continue
				}
				if len(pending) >= 1000 {
					partial = true
					break
				}
				pending = append(pending, child)
			}
		} else if stat.Type == sandbox.RemoteEntryFile {
			if len(files) >= 500 {
				partial = true
				break
			}
			files = append(files, searchableFile{name, func(ctx context.Context) ([]byte, error) {
				current, err := t.workspace.source.StatSessionFile(ctx, sid, name)
				if err != nil {
					return nil, err
				}
				if current == nil || current.Type != sandbox.RemoteEntryFile ||
					current.Size > maxReadSandboxDownloadBytes {
					return nil, fmt.Errorf("not a readable regular file")
				}
				return t.workspace.readWithCache(ctx, sid, name, current)
			}})
		}
	}
	return files, partial, nil
}
