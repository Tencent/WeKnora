package skills

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/Tencent/WeKnora/internal/skillrunner"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/google/uuid"
)

type TenantRunner interface {
	Execute(context.Context, skillrunner.ExecuteRequest) (skillrunner.ExecuteResponse, error)
}

type TenantResolver struct {
	*TenantLoader
	repo   TenantSkillRepository
	runner TenantRunner
}

func NewTenantResolver(loader *TenantLoader, repo TenantSkillRepository, runner TenantRunner) *TenantResolver {
	return &TenantResolver{TenantLoader: loader, repo: repo, runner: runner}
}

func (resolver *TenantResolver) Execute(ctx context.Context, scope RuntimeScope, ref types.SkillReference, scriptPath string, args []string, stdin string) (*sandbox.ExecuteResult, error) {
	if ref.Source == types.SkillSourcePreloaded {
		return nil, fmt.Errorf("preloaded execution uses compatibility sandbox")
	}
	if !referenceAllowed(scope.Allowed, ref) {
		return nil, ErrSkillNotAllowed
	}
	version, skill, err := resolver.authorize(ctx, scope.TenantID, ref)
	if err != nil {
		return nil, err
	}
	if resolver.runner == nil {
		return nil, skillrunner.ErrRunnerUnavailable
	}
	executionID := uuid.NewString()
	started := time.Now()
	audit := &types.SkillExecutionAudit{
		ID: executionID, TenantID: scope.TenantID, SkillID: skill.ID, VersionID: version.ID,
		UserID: scope.UserID, ScriptPath: scriptPath, Status: "running", StartedAt: started,
	}
	if err := resolver.repo.CreateExecutionAudit(ctx, audit); err != nil {
		return nil, err
	}
	response, runErr := resolver.runner.Execute(ctx, skillrunner.ExecuteRequest{
		ExecutionID: executionID, TenantID: strconv.FormatUint(scope.TenantID, 10), SkillID: skill.ID,
		VersionID: version.ID, ContentHash: version.ContentHash, ScriptPath: scriptPath,
		Args: args, Stdin: stdin,
	})
	finished := time.Now()
	status := "completed"
	if runErr != nil {
		status = "failed"
	}
	finish := types.ExecutionAuditFinish{
		Status: status, FinishedAt: finished, DurationMS: finished.Sub(started).Milliseconds(),
		ExitCode: &response.ExitCode, Killed: response.Killed, Truncated: response.Truncated,
		OutputSummary: truncateAuditOutput(response.Stdout, response.Stderr),
	}
	if finishErr := resolver.repo.FinishExecutionAudit(ctx, scope.TenantID, executionID, finish); finishErr != nil && runErr == nil {
		return nil, finishErr
	}
	if runErr != nil {
		return nil, runErr
	}
	return &sandbox.ExecuteResult{
		ExitCode: response.ExitCode, Stdout: response.Stdout, Stderr: response.Stderr,
		Killed: response.Killed, Duration: finished.Sub(started),
	}, nil
}

func truncateAuditOutput(stdout, stderr string) string {
	value := stdout
	if stderr != "" {
		value += "\n" + stderr
	}
	if len(value) > 4096 {
		return value[:4096]
	}
	return value
}
