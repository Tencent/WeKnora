package admission

import (
	"crypto/sha256"
	"fmt"
	"os"

	"github.com/Tencent/WeKnora/internal/models/limiter"
	"github.com/Tencent/WeKnora/internal/types"
)

// Register binds a real provider endpoint before shared admission is used.
// Unsaved model connection probes receive a stable, credential-free identity.
func Register(id, name, endpoint string, source types.ModelSource) (string, error) {
	if !limiter.SharedRuntimeStats().Enabled {
		return id, nil
	}
	if endpoint == "" && source == types.ModelSourceLocal {
		endpoint = os.Getenv("OLLAMA_BASE_URL")
		if endpoint == "" {
			endpoint = "http://localhost:11434"
		}
	}
	group, err := limiter.EndpointGroup(endpoint)
	if err != nil {
		return "", err
	}
	if id == "" {
		id = fmt.Sprintf("temporary:%x", sha256.Sum256([]byte(group+"\x00"+name)))
	}
	if err := limiter.RegisterModelEndpoint(id, endpoint); err != nil {
		return "", err
	}
	return id, nil
}
