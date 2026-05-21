package output

import "io"

// BatchItem is one per-id outcome in a batch operation envelope (§4.5).
type BatchItem struct {
	ID     string     `json:"id"`
	OK     bool       `json:"ok"`
	Result any        `json:"result,omitempty"`
	Error  *ErrDetail `json:"error,omitempty"`
}

// WriteBatchEnvelope writes a batch operation envelope per §4.5.
//
// Wire shape: {ok, data:[BatchItem...], meta:{count, successes, failures}, profile?}.
// Top-level ok = (failures == 0). Per-id ok reflects each item's outcome.
// Even when all items fail, the response stays in success-envelope shape
// (data array, not error envelope) so agents can iterate detail per id.
// profile is the resolved profile name for the invocation; empty string omits the field.
func WriteBatchEnvelope(w io.Writer, items []BatchItem, indent bool, profile string) error {
	if items == nil {
		items = []BatchItem{}
	}
	successes, failures := 0, 0
	for _, it := range items {
		if it.OK {
			successes++
		} else {
			failures++
		}
	}
	env := Envelope{
		OK:   failures == 0,
		Data: items,
		Meta: &Meta{
			Count:     len(items),
			Successes: &successes,
			Failures:  &failures,
		},
		Notice:  GetNotice(),
		Profile: profile,
	}
	return writeJSON(w, env, indent)
}
