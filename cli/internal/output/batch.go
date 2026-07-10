package output

import "io"

// BatchItem is one per-id outcome in a batch operation envelope.
type BatchItem struct {
	ID     string     `json:"id"`
	OK     bool       `json:"ok"`
	Result any        `json:"result,omitempty"`
	Error  *ErrDetail `json:"error,omitempty"`
}

// WriteBatchEnvelope writes a batch operation envelope.
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
		OK:     failures == 0,
		Status: batchStatus(successes, failures),
		Data:   items,
		Meta: &Meta{
			Count:     IntPtr(len(items)),
			Successes: &successes,
			Failures:  &failures,
		},
		Profile: profile,
	}
	return writeJSON(w, env, indent)
}

// batchStatus maps the (successes, failures) counts to the tri-state envelope
// status: "success" when no failures, "error" when no successes, "partial"
// when both occur.
func batchStatus(successes, failures int) string {
	switch {
	case failures == 0:
		return "success"
	case successes == 0:
		return "error"
	default:
		return "partial"
	}
}
