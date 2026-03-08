package docparser

import (
	"context"
	"encoding/csv"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
)

// simpleFormats lists file extensions that Go can handle without the Python service.
var simpleFormats = map[string]bool{
	"md": true, "markdown": true,
	"txt": true, "text": true,
	"csv": true,
}

var imageFormats = map[string]bool{
	"jpg": true, "jpeg": true, "png": true, "gif": true,
	"bmp": true, "tiff": true, "webp": true,
}

func init() {
	for k := range imageFormats {
		simpleFormats[k] = true
	}
}

// IsSimpleFormat returns true if the file type can be handled by the Go SimpleFormatReader.
func IsSimpleFormat(fileType string) bool {
	return simpleFormats[strings.ToLower(strings.TrimPrefix(fileType, "."))]
}

// SimpleFormatReader handles simple file formats and images directly in Go,
// bypassing the Python docreader service.
type SimpleFormatReader struct{}

// Read reads simple format files and returns markdown.
func (b *SimpleFormatReader) Read(_ context.Context, req *types.ReadRequest) (*types.ReadResult, error) {
	ft := strings.ToLower(strings.TrimPrefix(req.FileType, "."))
	if ft == "" {
		ft = strings.TrimPrefix(strings.ToLower(filepath.Ext(req.FileName)), ".")
	}

	switch {
	case ft == "md" || ft == "markdown":
		return &types.ReadResult{MarkdownContent: string(req.FileContent)}, nil
	case ft == "txt" || ft == "text":
		return &types.ReadResult{MarkdownContent: string(req.FileContent)}, nil
	case ft == "csv":
		md, err := csvToMarkdown(req.FileContent)
		if err != nil {
			return nil, fmt.Errorf("csv conversion failed: %w", err)
		}
		return &types.ReadResult{MarkdownContent: md}, nil
	case imageFormats[ft]:
		return imageToResult(req.FileName, req.FileContent), nil
	default:
		return nil, fmt.Errorf("unsupported simple format: %s", ft)
	}
}

// imageToResult wraps a standalone image as a markdown image reference with
// the raw bytes in ImageRefs, matching Python ImageParser behaviour.
func imageToResult(fileName string, data []byte) *types.ReadResult {
	if fileName == "" {
		fileName = "image.png"
	}
	refPath := "images/" + fileName
	mime := http.DetectContentType(data)

	return &types.ReadResult{
		MarkdownContent: fmt.Sprintf("![%s](%s)", fileName, refPath),
		ImageRefs: []types.ImageRef{
			{
				Filename:    fileName,
				OriginalRef: refPath,
				MimeType:    mime,
				ImageData:   data,
			},
		},
	}
}

// IsImageFormat returns true if the file type is a recognized image format.
func IsImageFormat(fileType string) bool {
	return imageFormats[strings.ToLower(strings.TrimPrefix(fileType, "."))]
}

// ensureOriginalImageRef checks whether the input file is an image and, if the
// returned markdown does not already contain a markdown image reference for it,
// prepends one and appends the raw bytes to imageRefs. This guarantees that
// when MinerU OCRs a standalone image, the downstream chunks still carry the
// original image link for retrieval display.
func ensureOriginalImageRef(req *types.ReadRequest, mdContent string, imageRefs []types.ImageRef) (string, []types.ImageRef) {
	ft := strings.ToLower(strings.TrimPrefix(req.FileType, "."))
	if ft == "" {
		ft = strings.TrimPrefix(strings.ToLower(filepath.Ext(req.FileName)), ".")
	}
	if !imageFormats[ft] {
		return mdContent, imageRefs
	}
	if len(req.FileContent) == 0 {
		return mdContent, imageRefs
	}

	fileName := req.FileName
	if fileName == "" {
		fileName = "image." + ft
	}
	refPath := "images/" + fileName

	if strings.Contains(mdContent, refPath) {
		return mdContent, imageRefs
	}

	imgLine := fmt.Sprintf("![%s](%s)", fileName, refPath)
	if strings.TrimSpace(mdContent) == "" {
		mdContent = imgLine
	} else {
		mdContent = imgLine + "\n\n" + mdContent
	}

	mime := http.DetectContentType(req.FileContent)
	imageRefs = append(imageRefs, types.ImageRef{
		Filename:    fileName,
		OriginalRef: refPath,
		MimeType:    mime,
		ImageData:   req.FileContent,
	})

	return mdContent, imageRefs
}

func csvToMarkdown(data []byte) (string, error) {
	reader := csv.NewReader(strings.NewReader(string(data)))
	reader.LazyQuotes = true
	reader.TrimLeadingSpace = true

	records, err := reader.ReadAll()
	if err != nil {
		return "", err
	}
	if len(records) == 0 {
		return "", nil
	}

	var sb strings.Builder

	// Header row
	header := records[0]
	sb.WriteString("| ")
	sb.WriteString(strings.Join(header, " | "))
	sb.WriteString(" |\n")

	// Separator
	sb.WriteString("|")
	for range header {
		sb.WriteString(" --- |")
	}
	sb.WriteString("\n")

	// Data rows
	for _, row := range records[1:] {
		sb.WriteString("| ")
		// Pad row if shorter than header
		cells := make([]string, len(header))
		for i := range cells {
			if i < len(row) {
				cells[i] = row[i]
			}
		}
		sb.WriteString(strings.Join(cells, " | "))
		sb.WriteString(" |\n")
	}

	return sb.String(), nil
}
