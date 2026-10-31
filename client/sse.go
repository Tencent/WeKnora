package client

import (
	"fmt"
	"io"
	"strings"
)

func appendSSEDataLine(buffer, line string) string {
	return buffer + strings.TrimPrefix(line[5:], " ") + "\n"
}

func completeSSEData(buffer string) string {
	return strings.TrimSuffix(buffer, "\n")
}

// A data frame is dispatched only by its blank line. Do not silently report
// success if the connection ended with undispatched data.
func incompleteSSEDataError(buffer string) error {
	if completeSSEData(buffer) == "" {
		return nil
	}
	return fmt.Errorf("incomplete SSE data frame: %w", io.ErrUnexpectedEOF)
}
