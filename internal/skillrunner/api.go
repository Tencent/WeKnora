package skillrunner

import "errors"

var (
	ErrInvalidRequest    = errors.New("skill runner: invalid request")
	ErrUnauthorized      = errors.New("skill runner: unauthorized")
	ErrRunnerUnavailable = errors.New("skill runner: unavailable")
)

type ExecuteRequest struct {
	ExecutionID string   `json:"execution_id"`
	TenantID    string   `json:"tenant_id"`
	SkillID     string   `json:"skill_id"`
	VersionID   string   `json:"version_id"`
	ContentHash string   `json:"content_hash"`
	ScriptPath  string   `json:"script_path"`
	Args        []string `json:"args"`
	Stdin       string   `json:"stdin"`
}

type ExecuteResponse struct {
	ExitCode  int    `json:"exit_code"`
	Stdout    string `json:"stdout"`
	Stderr    string `json:"stderr"`
	Truncated bool   `json:"truncated"`
	Killed    bool   `json:"killed"`
}

type ResolvedVersion struct {
	SourcePath     string
	ContentHash    string
	SourceVolume   string
	AllowedScripts []string
}
