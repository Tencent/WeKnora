package httpx

import (
	"github.com/Tencent/WeKnora/internal/models/utils"
)

// Sign is a thin pass-through to the WeKnoraCloud signer that lives in
// internal/models/utils. It's re-exported here so LLM sub-packages only need
// to import one helper package (httpx), not two.
func Sign(appID, apiKey, requestID, bodyJSON string) map[string]string {
	return utils.Sign(appID, apiKey, requestID, bodyJSON)
}
