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
		return ErrorCategoryTimeout, "timeout"
	}
	message := strings.ToLower(err.Error())
	switch {
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
	case containsAny(message, "parse ", "unmarshal", "decode ", "结果为空", "result payload is empty", "invalid timeline", "no non-empty timed sentences", "transcript contains no", "无有效文本"):
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
