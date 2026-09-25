package client

import "strings"

func appendSSEDataLine(buffer, line string) string {
	return buffer + strings.TrimPrefix(line[5:], " ") + "\n"
}

func completeSSEData(buffer string) string {
	return strings.TrimSuffix(buffer, "\n")
}
