package handler

import (
	"encoding/json"
	"errors"

	"github.com/Tencent/WeKnora/internal/types"
)

type taskReplaySpecV1 struct {
	Version   int               `json:"version"`
	Kind      types.TaskJobKind `json:"kind"`
	SourceRef struct {
		Type string `json:"type"`
		ID   string `json:"id"`
	} `json:"source_ref"`
	Scope struct {
		KnowledgeID     string `json:"knowledge_id"`
		KnowledgeBaseID string `json:"knowledge_base_id"`
	} `json:"scope"`
	ProcessConfig struct {
		FileName                 string `json:"file_name,omitempty"`
		FileType                 string `json:"file_type,omitempty"`
		EnableMultimodel         bool   `json:"enable_multimodel,omitempty"`
		EnableQuestionGeneration bool   `json:"enable_question_generation,omitempty"`
		QuestionCount            int    `json:"question_count,omitempty"`
		Language                 string `json:"language,omitempty"`
	} `json:"process_config"`
}

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
	if _, _, err := replayDocumentPayload(job); err != nil {
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

func replayDocumentPayload(job *types.TaskJob) (types.DocumentProcessPayload, string, error) {
	if job == nil || (job.Kind != types.TaskJobKindUpload && job.Kind != types.TaskJobKindReparse) {
		return types.DocumentProcessPayload{}, "", errors.New("unsupported task kind for retry")
	}
	var spec taskReplaySpecV1
	if err := json.Unmarshal(job.ReplaySpec, &spec); err != nil {
		return types.DocumentProcessPayload{}, "", errors.New("invalid replay spec")
	}
	if spec.Version != 1 || spec.Scope.KnowledgeID == "" || spec.Scope.KnowledgeBaseID == "" {
		return types.DocumentProcessPayload{}, "", errors.New("unsupported replay spec")
	}
	payload := types.DocumentProcessPayload{
		TenantID:                 job.TenantID,
		KnowledgeID:              spec.Scope.KnowledgeID,
		KnowledgeBaseID:          spec.Scope.KnowledgeBaseID,
		FileName:                 spec.ProcessConfig.FileName,
		FileType:                 spec.ProcessConfig.FileType,
		EnableMultimodel:         spec.ProcessConfig.EnableMultimodel,
		EnableQuestionGeneration: spec.ProcessConfig.EnableQuestionGeneration,
		QuestionCount:            spec.ProcessConfig.QuestionCount,
		Language:                 spec.ProcessConfig.Language,
	}
	switch spec.SourceRef.Type {
	case "object_storage":
		payload.FilePath = spec.SourceRef.ID
	case "file_url":
		payload.FileURL = spec.SourceRef.ID
	case "url":
		payload.URL = spec.SourceRef.ID
	default:
		return types.DocumentProcessPayload{}, "", errors.New("task source is not replayable")
	}
	return payload, types.QueueCritical, nil
}
