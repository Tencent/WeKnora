package worker

import (
	"context"
	"errors"
	"strings"
)

const (
	ErrorCategoryConfigurationAuth = "configuration_auth"
	ErrorCategoryExternalTask      = "external_task"
	ErrorCategoryResponseParse     = "response_parse"
	ErrorCategoryObjectStorage     = "object_storage"
	ErrorCategoryWeKnora           = "weknora"
	ErrorCategoryWikiArtifact      = "wiki_artifact"
	ErrorCategoryDatabase          = "database"
	ErrorCategoryTimeout           = "timeout"
	ErrorCategoryUnknown           = "unknown"
)

func ClassifyProcessingError(err error) (string, string) {
	if err == nil {
		return "", ""
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return ErrorCategoryTimeout, "llm_deadline_exceeded"
	}
	var connectionClosed interface{ ConnectionClosed() bool }
	if errors.As(err, &connectionClosed) && connectionClosed.ConnectionClosed() {
		return ErrorCategoryExternalTask, "llm_connection_closed"
	}
	var incompleteOutput interface{ IncompleteOutput() bool }
	if errors.As(err, &incompleteOutput) && incompleteOutput.IncompleteOutput() {
		return ErrorCategoryResponseParse, "llm_stream_incomplete"
	}
	var streamTimeout interface{ StreamTimeoutPhase() string }
	if errors.As(err, &streamTimeout) {
		return ErrorCategoryTimeout, "llm_stream_" + streamTimeout.StreamTimeoutPhase() + "_timeout"
	}
	message := strings.ToLower(err.Error())
	if containsAny(message, "p3 knowledge object validation failed", "knowledge object content contract", "knowledge object relation contract") {
		return ErrorCategoryWikiArtifact, "content_contract_failed"
	}
	if marker := "transcript_source_validation:"; strings.Contains(message, marker) {
		remainder := message[strings.Index(message, marker)+len(marker):]
		if end := strings.IndexByte(remainder, ':'); end >= 0 && strings.TrimSpace(remainder[:end]) != "" {
			return ErrorCategoryResponseParse, strings.TrimSpace(remainder[:end])
		}
		return ErrorCategoryResponseParse, "transcript_source_validation"
	}
	switch {
	case containsAny(message, "validate summary output", "validate summary classification", "resolve summary evidence"):
		return ErrorCategoryResponseParse, "summary_contract_invalid"
	case containsAny(message, "timeout", "deadline exceeded", "超时"):
		return ErrorCategoryTimeout, "timeout"
	case containsAny(message, "status 401", "status 403", "unauthorized", "forbidden", "invalidaccesskey", "signature", "鉴权失败", "认证失败"):
		return ErrorCategoryConfigurationAuth, "authentication_failed"
	case containsAny(message, "未配置", "missing config", "access key is empty", "api key is empty"):
		return ErrorCategoryConfigurationAuth, "configuration_missing"
	case containsAny(message, "未找到 job=", "wiki page not found", "wiki 产物", "artifact missing", "产物页"):
		return ErrorCategoryWikiArtifact, "wiki_artifact_missing"
	case containsAny(message, "upload srt", "put object", "object storage", "minio", "对象存储", "public url"):
		return ErrorCategoryObjectStorage, "object_storage_operation"
	case containsAny(message, "weknora", "knowledge ", "知识库", "create session", "trigger skill", "agent chat"):
		return ErrorCategoryWeKnora, "weknora_operation"
	case containsAny(message, "tsc.fileerror", "source file rejected"):
		return ErrorCategoryExternalTask, "source_file_rejected"
	case containsAny(message, "parse ", "unmarshal", "decode ", "validate outline output", "outline schema_version", "outline must ", "chapter ", "response does not contain a valid json object", "结果为空", "result payload is empty", "invalid timeline", "no non-empty timed sentences", "transcript contains no", "无有效文本"):
		return ErrorCategoryResponseParse, "response_parse"
	case containsAny(message, "tingwu", "听悟", "external task", "task failed", "rate limit", "限流"):
		return ErrorCategoryExternalTask, "external_task_failed"
	case containsAny(message, "database", "sql", "gorm", "db ", "数据库"):
		return ErrorCategoryDatabase, "database_operation"
	default:
		return ErrorCategoryUnknown, "processing_failed"
	}
}

func containsAny(message string, fragments ...string) bool {
	for _, fragment := range fragments {
		if strings.Contains(message, fragment) {
			return true
		}
	}
	return false
}
