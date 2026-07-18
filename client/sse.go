package client

import "strings"

// appendSSEData appends the payload of a single Server-Sent Events "data:" line
// to buf and returns the result.
//
// Per the WHATWG SSE specification, a single event may carry multiple "data:"
// lines whose payloads are concatenated with a newline ("\n") to form the final
// data value. The previous parsers overwrote the buffer on every "data:" line,
// so an event whose JSON payload was split across several "data:" lines was
// truncated to its last fragment and failed to parse. Accumulating the
// fragments here fixes that for every streaming path that uses it.
//
// The caller passes the raw line, which must start with the "data:" field name;
// a single optional leading space after the colon is stripped, also per spec.
func appendSSEData(buf, line string) string {
	payload := strings.TrimPrefix(line[len("data:"):], " ")
	if buf == "" {
		return payload
	}
	return buf + "\n" + payload
}
