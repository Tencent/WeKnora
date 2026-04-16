package vlm

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
)

// debugVLM wraps a VLM with LLM debug logging.
type debugVLM struct {
	inner VLM
}

func (d *debugVLM) Predict(ctx context.Context, imgBytes [][]byte, prompt string) (string, error) {
	start := time.Now()
	result, err := d.inner.Predict(ctx, imgBytes, prompt)
	logVLMDebug(ctx, d.inner.GetModelName(), imgBytes, prompt, result, err, time.Since(start))
	return result, err
}

func (d *debugVLM) GetModelName() string { return d.inner.GetModelName() }
func (d *debugVLM) GetModelID() string   { return d.inner.GetModelID() }

func logVLMDebug(ctx context.Context, model string, imgBytes [][]byte, prompt string, response string, callErr error, dur time.Duration) {
	if !logger.LLMDebugEnabled() {
		return
	}

	record := &logger.LLMCallRecord{
		CallType: "VLM",
		Model:    model,
		Duration: dur,
	}

	// Input section
	var inputBuf strings.Builder
	inputBuf.WriteString(fmt.Sprintf("Images: count=%d", len(imgBytes)))
	totalSize := 0
	for _, img := range imgBytes {
		totalSize += len(img)
	}
	inputBuf.WriteString(fmt.Sprintf(", total_size=%d bytes\n\n", totalSize))
	inputBuf.WriteString("[prompt]\n")
	inputBuf.WriteString(prompt)
	inputBuf.WriteString("\n")
	record.Sections = append(record.Sections, logger.RecordSection{Title: "Input", Content: inputBuf.String()})

	// Response section
	if response != "" {
		record.Sections = append(record.Sections, logger.RecordSection{
			Title:   "Response",
			Content: response,
		})
	}

	if callErr != nil {
		record.Error = callErr.Error()
	}
	logger.LLMDebugLog(ctx, record)
}
