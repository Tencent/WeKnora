package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
)

// logicalPathKnowledgeFinder is deliberately narrow while the resolver is
// introduced. Keeping it separate from KnowledgeRepository avoids widening
// every existing test double; the production repository implements it.
type logicalPathKnowledgeFinder interface {
	FindKnowledgeByLogicalPath(ctx context.Context, tenantID uint64, kbID, folderPath, fileName string) (*types.Knowledge, error)
}

// RewriteRelativeMarkdownImages rewrites inline Markdown image targets while
// leaving code spans and fenced code blocks untouched. It intentionally covers
// the P0 inline-image grammar only; ordinary links are a P1 extension.
func (s *knowledgeService) RewriteRelativeMarkdownImages(ctx context.Context, tenantID uint64, kbID, folderPath, markdown string) string {
	type replacement struct {
		start, end int
		value      string
	}
	var replacements []replacement
	for i := 0; i < len(markdown); {
		if isFenceStart(markdown, i) {
			length, ch := fenceRun(markdown, i)
			i = findFenceEnd(markdown, i+length, ch, length)
			continue
		}
		if markdown[i] == '`' {
			run := 1
			for i+run < len(markdown) && markdown[i+run] == '`' {
				run++
			}
			if close := findInlineCodeClose(markdown, i+run, run); close >= 0 {
				i = close + run
				continue
			}
		}
		if markdown[i] != '!' || i+1 >= len(markdown) || markdown[i+1] != '[' {
			i++
			continue
		}
		end, ok := matchMarkdownLink(markdown, i+1)
		if !ok {
			i++
			continue
		}
		open := strings.Index(markdown[i+1:end], "](")
		if open < 0 {
			i = end
			continue
		}
		targetStart := i + 1 + open + 2
		targetEnd := end - 1
		target := strings.TrimSpace(markdown[targetStart:targetEnd])
		if strings.HasPrefix(target, "<") {
			if close := strings.IndexByte(target, '>'); close > 0 {
				targetStart += 1
				targetEnd = targetStart + close - 1
			} else {
				i = end
				continue
			}
		} else if space := strings.IndexAny(target, " \t"); space >= 0 {
			targetEnd = targetStart + space
		}
		// Markdown permits a destination to escape parentheses. Resolve the
		// logical filename, not its Markdown escaping, while replacing the whole
		// destination with the canonical resource URL below.
		referencePath := strings.ReplaceAll(markdown[targetStart:targetEnd], `\(`, "(")
		referencePath = strings.ReplaceAll(referencePath, `\)`, ")")
		if ref, resolved, err := s.ResolveRelativeFileReference(ctx, tenantID, kbID, folderPath, referencePath); err == nil && resolved {
			replacements = append(replacements, replacement{targetStart, targetEnd, ref})
		}
		i = end
	}
	for i := len(replacements) - 1; i >= 0; i-- {
		r := replacements[i]
		markdown = markdown[:r.start] + r.value + markdown[r.end:]
	}
	return markdown
}

// ResolveRelativeFileReference resolves one document-relative path to the
// target Knowledge and its stable resource handle. It is shared by the parser
// and preview paths; callers leave the original reference unchanged when
// resolved is false.
func (s *knowledgeService) ResolveRelativeFileReference(
	ctx context.Context,
	tenantID uint64,
	knowledgeBaseID, currentFolderPath, referencePath string,
) (reference string, resolved bool, err error) {
	folderPath, fileName, ok := types.ResolveKnowledgeRelativeReference(currentFolderPath, referencePath)
	if !ok {
		return referencePath, false, nil
	}

	finder, ok := s.repo.(logicalPathKnowledgeFinder)
	if !ok {
		return referencePath, false, fmt.Errorf("knowledge repository does not support logical path lookup")
	}
	target, err := finder.FindKnowledgeByLogicalPath(ctx, tenantID, knowledgeBaseID, folderPath, fileName)
	if err != nil || target == nil {
		return referencePath, false, err
	}
	if _, ok := types.ParseResourcePath(target.FilePath); !ok {
		// A resource catalog may be disabled on legacy installations. Returning
		// unresolved is safer than embedding a provider path or fabricating a
		// handle that cannot be served to the browser.
		return referencePath, false, nil
	}
	return target.FilePath, true, nil
}
