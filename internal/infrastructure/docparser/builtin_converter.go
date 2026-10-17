package docparser

import (
	"bytes"
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
	"csv":  true,
	"json": true,
}

var imageFormats = map[string]bool{
	"jpg": true, "jpeg": true, "png": true, "gif": true,
	"bmp": true, "tiff": true, "webp": true,
}

var audioFormats = map[string]bool{
	"mp3": true, "wav": true, "m4a": true, "flac": true, "ogg": true,
}

func init() {
	for k := range imageFormats {
		simpleFormats[k] = true
	}
	for k := range audioFormats {
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
	case ft == "json":
		md, err := jsonToMarkdown(req.FileContent)
		if err != nil {
			return nil, fmt.Errorf("json conversion failed: %w", err)
		}
		return &types.ReadResult{MarkdownContent: md}, nil
	case imageFormats[ft]:
		return imageToResult(req.FileName, req.FileContent), nil
	case audioFormats[ft]:
		return audioToResult(req.FileName, req.FileContent), nil
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
	// Encode spaces so the markdown URL is valid and matches the regex in ResolveAndStore.
	safeRef := strings.ReplaceAll(refPath, " ", "%20")
	mime := http.DetectContentType(data)

	return &types.ReadResult{
		MarkdownContent: fmt.Sprintf("![%s](%s)", fileName, safeRef),
		ImageRefs: []types.ImageRef{
			{
				Filename:    fileName,
				OriginalRef: safeRef,
				MimeType:    mime,
				ImageData:   data,
				IsOriginal:  true,
			},
		},
	}
}

// IsImageFormat returns true if the file type is a recognized image format.
func IsImageFormat(fileType string) bool {
	return imageFormats[strings.ToLower(strings.TrimPrefix(fileType, "."))]
}

// IsAudioFormat returns true if the file type is a recognized audio format.
func IsAudioFormat(fileType string) bool {
	return audioFormats[strings.ToLower(strings.TrimPrefix(fileType, "."))]
}

// audioToResult wraps a standalone audio file. The actual transcription is
// handled by the ASR model in the knowledge service pipeline. Here we just
// return a placeholder markdown with the raw bytes preserved for upstream
// processing.
func audioToResult(fileName string, data []byte) *types.ReadResult {
	if fileName == "" {
		fileName = "audio.mp3"
	}
	// Return a placeholder; the knowledge service will replace this with
	// the ASR transcription result.
	return &types.ReadResult{
		MarkdownContent: fmt.Sprintf("[Audio file: %s]", fileName),
		IsAudio:         true,
		AudioData:       data,
	}
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
		IsOriginal:  true,
	})

	return mdContent, imageRefs
}

func csvToMarkdown(data []byte) (string, error) {
	// Excel's "CSV UTF-8" export starts the file with a byte order mark.
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	reader := csv.NewReader(bytes.NewReader(data))
	reader.LazyQuotes = true
	reader.TrimLeadingSpace = true
	// Rows may differ in length. The default, the first row's length, made
	// one short or long row fail the whole file.
	reader.FieldsPerRecord = -1

	records, err := reader.ReadAll()
	if err != nil {
		return "", err
	}
	if len(records) == 0 {
		return "", nil
	}

	// The table is as wide as the widest row, so no field is dropped, and
	// shorter rows are padded with empty cells.
	width := 0
	for _, record := range records {
		width = max(width, len(record))
	}

	var sb strings.Builder
	writeRow := func(record []string) {
		cells := make([]string, width)
		for i, cell := range record {
			cells[i] = csvCellReplacer.Replace(cell)
		}
		sb.WriteString("| ")
		sb.WriteString(strings.Join(cells, " | "))
		sb.WriteString(" |\n")
	}

	// Header row
	writeRow(records[0])

	// Separator
	sb.WriteString("|")
	for range width {
		sb.WriteString(" --- |")
	}
	sb.WriteString("\n")

	// Data rows
	for _, row := range records[1:] {
		writeRow(row)
	}

	return sb.String(), nil
}

// csvCellReplacer escapes what would end a GFM table cell or row: a pipe, and
// the line breaks of a quoted multi-line field. They become <br />, as the
// html-to-markdown table plugin writes them.
// Escape existing backslashes too, so a literal backslash before a pipe does
// not consume the pipe's escape and turn it back into a column separator.
var csvCellReplacer = strings.NewReplacer(`\`, `\\`, "|", `\|`, "\r\n", "<br />", "\n", "<br />", "\r", "<br />")
