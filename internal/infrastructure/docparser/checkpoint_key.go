package docparser

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/Tencent/WeKnora/internal/types"
)

// ReadCheckpointKey identifies parser inputs without allocating a base64 copy
// of a large PDF. Request IDs change on retry and do not affect parsing.
func ReadCheckpointKey(req *types.ReadRequest) string {
	metadata := *req
	metadata.FileContent = nil
	metadata.RequestID = ""
	encoded, _ := json.Marshal(metadata)
	h := sha256.New()
	_, _ = fmt.Fprintf(h, "%d:", len(req.FileContent))
	h.Write(req.FileContent)
	h.Write(encoded)
	return hex.EncodeToString(h.Sum(nil))
}
