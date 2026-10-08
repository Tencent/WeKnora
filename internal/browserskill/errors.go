package browserskill

import "encoding/json"

// RPCError preserves the daemon's error classification across local and cluster
// transports. Error keeps the legacy string contract for older peers and callers.
type RPCError struct {
	Code    string          `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *RPCError) Error() string { return e.Code + ": " + e.Message }

// BoundDetails keeps useful recovery evidence without forwarding an unbounded
// page-controlled payload into model context. Oversized details retain only the
// protocol's recovery discriminators.
func (e *RPCError) BoundDetails() {
	if len(e.Data) <= 8192 {
		return
	}
	var data map[string]json.RawMessage
	_ = json.Unmarshal(e.Data, &data)
	bounded := map[string]any{"truncated": true}
	for _, key := range []string{"reason", "effect_state"} {
		var value string
		if json.Unmarshal(data[key], &value) == nil && len(value) <= 128 {
			bounded[key] = value
		}
	}
	e.Data, _ = json.Marshal(bounded)
}
