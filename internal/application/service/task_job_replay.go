package service

import (
	"encoding/json"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/hibiken/asynq"
)

type taskReplaySpecV2 struct {
	Version  int              `json:"version"`
	TaskType string           `json:"task_type"`
	JobID    string           `json:"job_id"`
	Attempt  int              `json:"attempt"`
	Payload  json.RawMessage  `json:"payload"`
	Policy   taskReplayPolicy `json:"policy"`

	Kind          types.TaskJobKind   `json:"kind,omitempty"`
	SourceRef     replaySourceRef     `json:"source_ref,omitempty"`
	Scope         replayScope         `json:"scope,omitempty"`
	ProcessConfig replayProcessConfig `json:"process_config,omitempty"`
}

type taskReplayPolicy struct {
	Queue          string `json:"queue,omitempty"`
	MaxRetry       *int   `json:"max_retry,omitempty"`
	TimeoutMillis  int64  `json:"timeout_millis,omitempty"`
	DeadlineUnixMs int64  `json:"deadline_unix_ms,omitempty"`
}

type replaySourceRef struct {
	Type string `json:"type,omitempty"`
	ID   string `json:"id,omitempty"`
}

type replayScope struct {
	KnowledgeID     string `json:"knowledge_id,omitempty"`
	KnowledgeBaseID string `json:"knowledge_base_id,omitempty"`
}

type replayProcessConfig struct {
	FileName                 string `json:"file_name,omitempty"`
	FileType                 string `json:"file_type,omitempty"`
	EnableMultimodel         bool   `json:"enable_multimodel,omitempty"`
	EnableQuestionGeneration bool   `json:"enable_question_generation,omitempty"`
	QuestionCount            int    `json:"question_count,omitempty"`
	Language                 string `json:"language,omitempty"`
}

func newTaskReplaySpec(
	taskType string,
	payload []byte,
	jobID string,
	attempt int,
	opts []asynq.Option,
	legacy func(*taskReplaySpecV2),
) types.JSON {
	spec := taskReplaySpecV2{
		Version:  2,
		TaskType: taskType,
		JobID:    jobID,
		Attempt:  attempt,
		Payload:  append(json.RawMessage(nil), payload...),
		Policy:   replayPolicyFromOptions(opts),
	}
	if spec.Policy.Queue == "" {
		spec.Policy.Queue = types.QueueDefault
	}
	if legacy != nil {
		legacy(&spec)
	}
	return taskJobJSON(spec)
}

func replayPolicyFromOptions(opts []asynq.Option) taskReplayPolicy {
	p := taskReplayPolicy{Queue: types.QueueDefault}
	for _, opt := range opts {
		switch opt.Type() {
		case asynq.QueueOpt:
			if v, ok := opt.Value().(string); ok && v != "" {
				p.Queue = v
			}
		case asynq.MaxRetryOpt:
			if v, ok := opt.Value().(int); ok {
				p.MaxRetry = &v
			}
		case asynq.TimeoutOpt:
			if v, ok := opt.Value().(time.Duration); ok {
				p.TimeoutMillis = int64(v / time.Millisecond)
			}
		case asynq.DeadlineOpt:
			if v, ok := opt.Value().(time.Time); ok && !v.IsZero() {
				p.DeadlineUnixMs = v.UnixMilli()
			}
		}
	}
	return p
}

func replayOptionsFromPolicy(p taskReplayPolicy, extra ...asynq.Option) []asynq.Option {
	opts := make([]asynq.Option, 0, 4+len(extra))
	if p.Queue != "" {
		opts = append(opts, asynq.Queue(p.Queue))
	}
	if p.MaxRetry != nil {
		opts = append(opts, asynq.MaxRetry(*p.MaxRetry))
	}
	if p.TimeoutMillis > 0 {
		opts = append(opts, asynq.Timeout(time.Duration(p.TimeoutMillis)*time.Millisecond))
	}
	if p.DeadlineUnixMs > 0 {
		opts = append(opts, asynq.Deadline(time.UnixMilli(p.DeadlineUnixMs)))
	}
	opts = append(opts, extra...)
	return opts
}
