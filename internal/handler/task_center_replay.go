package handler

import (
	"github.com/Tencent/WeKnora/internal/types"
)

func canRetryJob(job *types.TaskJob, execs []*types.TaskExecution) bool {
	if job == nil {
		return false
	}
	if job.State != types.TaskJobStateFailed && job.State != types.TaskJobStateCanceled {
		return false
	}
	if !retryableTaskJobErrorClass(job.LastErrorClass) {
		return false
	}
	for _, exec := range execs {
		if exec != nil && !types.TaskExecutionIsTerminal(exec.State) {
			return false
		}
	}
	return true
}

func retryableTaskJobErrorClass(class types.TaskErrorClass) bool {
	switch class {
	case "", types.TaskErrorClassRetryable, types.TaskErrorClassCanceled, types.TaskErrorClassEnqueueFailed:
		return true
	default:
		return false
	}
}

func canCancelJob(job *types.TaskJob) bool {
	if job == nil || types.TaskJobIsTerminal(job.State) {
		return false
	}
	if job.Scope != types.TaskScopeKnowledge {
		return false
	}
	return job.Kind == types.TaskJobKindUpload || job.Kind == types.TaskJobKindReparse
}
