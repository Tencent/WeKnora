package tools

import (
	"context"
	"io"
	"os"

	"github.com/Tencent/WeKnora/internal/utils/textencoding"
)

// Keep stored uploads intact, as with prepareExcelForDuckDB. UTF-8 files use
// their original path; legacy text is streamed into a private temporary copy.
func prepareCSVForDuckDB(ctx context.Context, filename string) (string, func(), error) {
	noop := func() {}
	input, err := os.Open(filename)
	if err != nil {
		return "", noop, err
	}
	defer func() { _ = input.Close() }()
	reader, converted, err := textencoding.NewReader(ctx, input)
	if err != nil {
		return "", noop, err
	}
	if !converted {
		return filename, noop, nil
	}
	output, err := os.CreateTemp("", "weknora-csv-*.csv")
	if err != nil {
		return "", noop, err
	}
	cleanup := func() { _ = os.Remove(output.Name()) }
	if _, err := io.Copy(output, reader); err != nil {
		_ = output.Close()
		cleanup()
		return "", noop, err
	}
	if err := output.Close(); err != nil {
		cleanup()
		return "", noop, err
	}
	return output.Name(), cleanup, nil
}
