package docparser

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
)

type pdfPart struct {
	content     []byte
	first, last int
}

// splitCloudPDF bounds both page count and upload size, without rasterizing PDFs.
func splitCloudPDF(ctx context.Context, content []byte) ([]pdfPart, error) {
	dir, err := os.MkdirTemp("", "mineru-pdf-")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	input := filepath.Join(dir, "input.pdf")
	if err = os.WriteFile(input, content, 0o600); err != nil {
		return nil, err
	}
	out, err := exec.CommandContext(ctx, "qpdf", "--show-npages", input).Output()
	if err != nil {
		if ex, ok := err.(*exec.ExitError); !ok || ex.ExitCode() != 3 {
			return nil, fmt.Errorf("PDF page inspection: %w", err)
		}
	}
	pages, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil || pages < 1 {
		return nil, fmt.Errorf("invalid PDF page count %q", out)
	}
	if pages <= 100 && len(content) <= 10*1024*1024 {
		return []pdfPart{{content, 1, pages}}, nil
	}
	var parts []pdfPart
	var extract func(int, int) error
	extract = func(first, last int) error {
		path := filepath.Join(dir, fmt.Sprintf("%d-%d.pdf", first, last))
		output, e := exec.CommandContext(ctx, "qpdf", "--deterministic-id", input, "--pages", ".",
			fmt.Sprintf("%d-%d", first, last), "--", path).CombinedOutput()
		// qpdf exit 3 means a repaired PDF was written with warnings.
		if e != nil {
			if ex, ok := e.(*exec.ExitError); !ok || ex.ExitCode() != 3 {
				return fmt.Errorf("split PDF pages %d-%d: %s: %w", first, last, output, e)
			}
		}
		b, e := os.ReadFile(path)
		if e != nil {
			return e
		}
		_ = os.Remove(path)
		if len(b) > 10*1024*1024 && first < last {
			mid := (first + last) / 2
			if e = extract(first, mid); e != nil {
				return e
			}
			return extract(mid+1, last)
		}
		parts = append(parts, pdfPart{b, first, last})
		return nil
	}
	for first := 1; first <= pages; first += 100 {
		last := first + 99
		if last > pages {
			last = pages
		}
		if err = extract(first, last); err != nil {
			return nil, err
		}
	}
	return parts, nil
}

func (c *MinerUCloudReader) readCloudPart(ctx context.Context, req *types.ReadRequest) (*types.ReadResult, error) {
	batch, url, err := c.applyUploadURLs(ctx, req.FileName, filepath.Ext(req.FileName))
	if err != nil {
		return nil, fmt.Errorf("MinerU Cloud apply upload URLs: %w", err)
	}
	if err = c.uploadFile(ctx, url, req.FileContent); err != nil {
		return nil, fmt.Errorf("MinerU Cloud file upload: %w", err)
	}
	md, refs, contentList, err := c.pollBatchResult(ctx, batch)
	if err != nil {
		return nil, fmt.Errorf("MinerU Cloud poll: %w", err)
	}
	return &types.ReadResult{MarkdownContent: md, ImageRefs: refs, SourceBlocks: minerUSourceBlocks(md, contentList, req.FileType)}, nil
}

func (c *MinerUCloudReader) readSplitPDF(ctx context.Context, req *types.ReadRequest) (*types.ReadResult, error) {
	// Standalone/desktop deployments can still parse small PDFs without qpdf.
	// Docker includes qpdf; other deployments need it for automatic splitting.
	if _, err := exec.LookPath("qpdf"); err != nil {
		result, readErr := c.readCloudPart(ctx, req)
		if readErr != nil && strings.Contains(strings.ToLower(readErr.Error()), "exceeds limit") {
			return nil, fmt.Errorf("install qpdf to automatically split this large PDF: %w", readErr)
		}
		return result, readErr
	}
	parts, err := splitCloudPDF(ctx, req.FileContent)
	if err != nil {
		return nil, err
	}
	logger.Infof(ctx, "[MinerUCloud] PDF split into %d parts: %s", len(parts), req.FileName)
	merged := &types.ReadResult{Metadata: map[string]string{
		"pages": strconv.Itoa(parts[len(parts)-1].last), "ocr_parts": strconv.Itoa(len(parts)),
	}}
	var markdown strings.Builder
	for i, part := range parts {
		sub := *req
		sub.FileContent = part.content
		sub.FileName = fmt.Sprintf("%s-pages-%d-%d.pdf",
			strings.TrimSuffix(req.FileName, filepath.Ext(req.FileName)), part.first, part.last)
		result, e := c.readCloudPart(ctx, &sub)
		if e != nil {
			return nil, fmt.Errorf("OCR part %d/%d (pages %d-%d): %w", i+1, len(parts), part.first, part.last, e)
		}
		md := result.MarkdownContent
		blocks := append([]types.SourceBlock(nil), result.SourceBlocks...)
		// MinerU parts can reuse image filenames. Namespace references before merging.
		for _, ref := range result.ImageRefs {
			old := ref.OriginalRef
			ref.Filename = fmt.Sprintf("part-%d-%s", i+1, ref.Filename)
			ref.OriginalRef = fmt.Sprintf("part-%d/%s", i+1, old)
			runes := []rune(md)
			for j := range blocks {
				block := &blocks[j]
				start := utf8.RuneCountInString(replacePartImageRef(string(runes[:block.Start]), old, ref.OriginalRef))
				end := utf8.RuneCountInString(replacePartImageRef(string(runes[:block.End]), old, ref.OriginalRef))
				block.Start, block.End = start, end
			}
			md = replacePartImageRef(md, old, ref.OriginalRef)
			merged.ImageRefs = append(merged.ImageRefs, ref)
		}
		if i > 0 {
			markdown.WriteString("\n\n")
		}
		fmt.Fprintf(&markdown, "<!-- Original PDF pages %d-%d -->\n\n", part.first, part.last)
		offset := utf8.RuneCountInString(markdown.String())
		for _, block := range blocks {
			block.Locator.Page += part.first - 1
			block.Locator.SourceID = fmt.Sprintf("part-%d/%s", i+1, block.Locator.SourceID)
			block.Start += offset
			block.End += offset
			merged.SourceBlocks = append(merged.SourceBlocks, block)
		}
		markdown.WriteString(md)
		logger.Infof(ctx, "[MinerUCloud] Completed part %d/%d pages %d-%d", i+1, len(parts), part.first, part.last)
	}
	merged.MarkdownContent = markdown.String()
	return merged, nil
}

func replacePartImageRef(markdown, old, replacement string) string {
	markdown = strings.ReplaceAll(markdown, "]("+old+")", "]("+replacement+")")
	markdown = strings.ReplaceAll(markdown, `src="`+old+`"`, `src="`+replacement+`"`)
	return strings.ReplaceAll(markdown, "src='"+old+"'", "src='"+replacement+"'")
}
