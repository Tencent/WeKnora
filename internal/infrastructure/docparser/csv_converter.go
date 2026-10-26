package docparser

import (
	"bytes"
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"strings"
)

// Match the maximum size of a single Markdown entry extracted from a parser
// result package. The upload cap alone cannot bound padding of ragged CSV rows.
const maxCSVMarkdownBytes = maxZipEntryBytes

// ErrCSVMarkdownTooLarge marks deterministic CSV expansion failures.
// Callers should report the failure without retrying the same document.
var ErrCSVMarkdownTooLarge = errors.New("CSV conversion exceeds maximum Markdown size")

func csvToMarkdown(data []byte) (string, error) {
	return csvToMarkdownContext(context.Background(), data, maxCSVMarkdownBytes)
}

func csvToMarkdownContext(ctx context.Context, data []byte, limit int) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	// Excel's "CSV UTF-8" export starts with a byte order mark.
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	rows, width, contentBytes := 0, 0, 0
	reader := newCSVReader(ctx, data)
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		record, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}
		rows++
		width = max(width, len(record))
		for _, cell := range record {
			size, err := csvEscapedSize(ctx, cell, limit-contentBytes)
			if errors.Is(err, ErrCSVMarkdownTooLarge) {
				return "", csvSizeError(limit)
			}
			if err != nil {
				return "", err
			}
			contentBytes += size
		}
		// Check after every row: a late wide row also widens all preceding rows.
		if _, err := csvMarkdownSize(rows, width, contentBytes, limit); err != nil {
			return "", err
		}
	}
	if rows == 0 {
		return "", nil
	}
	size, err := csvMarkdownSize(rows, width, contentBytes, limit)
	if err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}

	// Re-read the input rather than retain every record. Grow only after the
	// entire output has passed preflight; rejected inputs allocate no table.
	var out strings.Builder
	out.Grow(size)
	reader = newCSVReader(ctx, data)
	for row := 0; row < rows; row++ {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		record, err := reader.Read()
		if err != nil {
			return "", err
		}
		out.WriteString("| ")
		for col := 0; col < width; col++ {
			if col%1024 == 0 {
				if err := ctx.Err(); err != nil {
					return "", err
				}
			}
			if col > 0 {
				out.WriteString(" | ")
			}
			if col < len(record) {
				writer := csvContextWriter{ctx: ctx, out: &out}
				if _, err := csvCellReplacer.WriteString(writer, record[col]); err != nil {
					return "", err
				}
			}
		}
		out.WriteString(" |\n")
		if row == 0 {
			out.WriteByte('|')
			for col := 0; col < width; col++ {
				if col%1024 == 0 {
					if err := ctx.Err(); err != nil {
						return "", err
					}
				}
				out.WriteString(" --- |")
			}
			out.WriteByte('\n')
		}
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return out.String(), nil
}

func csvSizeError(limit int) error {
	return fmt.Errorf("%w: table would render beyond the limit (%d bytes); reduce the number of rows or columns",
		ErrCSVMarkdownTooLarge, limit)
}

// A row needs 3*width+2 framing bytes; the separator needs 6*width+2.
// Divide before multiplying so even extreme row/column counts cannot overflow.
func csvMarkdownSize(rows, width, contentBytes, limit int) (int, error) {
	if limit < 2 || width > (limit-2)/6 {
		return 0, csvSizeError(limit)
	}
	size := 6*width + 2
	if contentBytes > limit-size {
		return 0, csvSizeError(limit)
	}
	size += contentBytes
	rowBytes := 3*width + 2
	if rows > (limit-size)/rowBytes {
		return 0, csvSizeError(limit)
	}
	return size + rows*rowBytes, nil
}

// Count the exact bytes csvCellReplacer will emit without allocating the
// escaped string. CRLF is replaced once, matching strings.Replacer's ordering.
func csvEscapedSize(ctx context.Context, cell string, remaining int) (int, error) {
	size := 0
	for i := 0; i < len(cell); i++ {
		if i%4096 == 0 {
			if err := ctx.Err(); err != nil {
				return 0, err
			}
		}
		n := 1
		switch cell[i] {
		case '\\', '|':
			n = 2
		case '\r':
			n = 6
			if i+1 < len(cell) && cell[i+1] == '\n' {
				i++
			}
		case '\n':
			n = 6
		}
		if n > remaining-size {
			return 0, ErrCSVMarkdownTooLarge
		}
		size += n
	}
	return size, nil
}

func newCSVReader(ctx context.Context, data []byte) *csv.Reader {
	reader := csv.NewReader(csvContextReader{ctx: ctx, reader: bytes.NewReader(data)})
	reader.LazyQuotes = true
	reader.TrimLeadingSpace = true
	reader.FieldsPerRecord = -1
	reader.ReuseRecord = true
	return reader
}

type csvContextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r csvContextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}

type csvContextWriter struct {
	ctx context.Context
	out *strings.Builder
}

func (w csvContextWriter) Write(p []byte) (int, error) {
	if err := w.ctx.Err(); err != nil {
		return 0, err
	}
	return w.out.Write(p)
}

func (w csvContextWriter) WriteString(s string) (int, error) {
	if err := w.ctx.Err(); err != nil {
		return 0, err
	}
	return w.out.WriteString(s)
}
