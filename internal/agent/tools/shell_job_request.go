package tools

import (
	"time"

	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// ShellJobSubmitRequest is the frozen submit payload shared by the remote
// shell-job service and the async shell tool.
type ShellJobSubmitRequest struct {
	Command         string
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

// ShellJobSubmission is the direct result of one SubmitShellJob call.
type ShellJobSubmission struct {
	Result     interfaces.WorkResult
	Background bool
}
