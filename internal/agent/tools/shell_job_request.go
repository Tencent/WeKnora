package tools

import (
	"time"

	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/Tencent/WeKnora/internal/utils"
)

// ShellJobSubmitRequest is the frozen submit payload shared by the remote
// shell-job service and the async shell tool.
type ShellJobSubmitRequest struct {
	Command string
	// DisplayCommand is the original command, before stdin, cwd and skill
	// wrappers. Only this command belongs in persistent metadata and results.
	DisplayCommand  string
	WorkDir         string
	Env             map[string]string
	Description     string
	YieldTime       time.Duration
	ParentMessageID string
	ToolCallID      string
	RunID           string
	OnOutput        func(string, []byte)
	// OutputDir is the deliverable directory, same as shell_exec. OutputSeen
	// reports that OutputBefore is a complete listing taken before the command.
	OutputDir    string
	OutputSeen   bool
	OutputBefore []sandbox.OutputFileStamp
}

// MaskedCommand keeps execution-only wrappers out of metadata. Direct service
// callers without a separate display command retain the existing fallback.
func (r ShellJobSubmitRequest) MaskedCommand() string {
	command := r.DisplayCommand
	if command == "" {
		command = r.Command
	}
	return utils.MaskCommandAssignments(command)
}

// ShellJobSubmission is the direct result of one SubmitShellJob call.
type ShellJobSubmission struct {
	Result     interfaces.WorkResult
	Background bool
}
