package client

import "strings"

func appendSSEDataLine(buffer, line string) string {
	data := line[5:]
	if strings.HasPrefix(data, " ") {
		data = data[1:]
	}
	return buffer + data + "\n"
}

func completeSSEData(buffer string) string {
	return strings.TrimSuffix(buffer, "\n")
}
