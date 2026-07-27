package skillrunner

import (
	"fmt"
	"path"
	"regexp"
	"strings"
)

const (
	MaxRequestBytes = 1 << 20
	MaxArgs         = 32
	MaxArgBytes     = 4096
	MaxStdinBytes   = 256 << 10
	MaxOutputBytes  = 1 << 20
)

var safeID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)

func ValidateRequest(request ExecuteRequest) error {
	for _, id := range []string{request.ExecutionID, request.TenantID, request.SkillID, request.VersionID} {
		if !safeID.MatchString(id) {
			return fmt.Errorf("%w: invalid identity", ErrInvalidRequest)
		}
	}
	if len(request.ContentHash) != 64 {
		return fmt.Errorf("%w: invalid content hash", ErrInvalidRequest)
	}
	clean := path.Clean(strings.ReplaceAll(request.ScriptPath, `\`, "/"))
	if clean != request.ScriptPath || strings.HasPrefix(clean, "../") || path.IsAbs(clean) {
		return fmt.Errorf("%w: invalid script path", ErrInvalidRequest)
	}
	switch path.Ext(clean) {
	case ".py", ".sh", ".js":
	default:
		return fmt.Errorf("%w: unsupported script", ErrInvalidRequest)
	}
	if len(request.Args) > MaxArgs || len(request.Stdin) > MaxStdinBytes {
		return ErrInvalidRequest
	}
	total := 0
	for _, arg := range request.Args {
		total += len(arg)
		if strings.IndexByte(arg, 0) >= 0 || total > MaxArgBytes {
			return ErrInvalidRequest
		}
	}
	return nil
}
