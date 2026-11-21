package errors

import (
	stderrors "errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func TestConstructors(t *testing.T) {
	details := map[string]any{"model": "m1", "refs": 2}

	tests := []struct {
		name       string
		got        *AppError
		wantCode   ErrorCode
		wantHTTP   int
		wantMsg    string
		wantDetail any
	}{
		{
			"BadRequest", NewBadRequestError("bad input"),
			ErrBadRequest, http.StatusBadRequest, "bad input", nil,
		},
		{
			"Unauthorized", NewUnauthorizedError("no token"),
			ErrUnauthorized, http.StatusUnauthorized, "no token", nil,
		},
		{
			"Forbidden", NewForbiddenError("denied"),
			ErrForbidden, http.StatusForbidden, "denied", nil,
		},
		{
			"NotFound", NewNotFoundError("missing"),
			ErrNotFound, http.StatusNotFound, "missing", nil,
		},
		{
			"Conflict", NewConflictError("exists"),
			ErrConflict, http.StatusConflict, "exists", nil,
		},
		{
			"Validation", NewValidationError("bad field"),
			ErrValidation, http.StatusBadRequest, "bad field", nil,
		},

		{
			"TooManyRequests custom", NewTooManyRequestsError("slow down"),
			ErrTooManyRequests, http.StatusTooManyRequests, "slow down", nil,
		},
		{
			"TooManyRequests default", NewTooManyRequestsError(""),
			ErrTooManyRequests, http.StatusTooManyRequests, "too many requests", nil,
		},
		{
			"Internal custom", NewInternalServerError("boom"),
			ErrInternalServer, http.StatusInternalServerError, "boom", nil,
		},
		{
			"Internal default", NewInternalServerError(""),
			ErrInternalServer, http.StatusInternalServerError, "服务器内部错误", nil,
		},
		{
			"ServiceUnavailable custom", NewServiceUnavailableError("down"),
			ErrServiceUnavailable, http.StatusServiceUnavailable, "down", nil,
		},
		{
			"ServiceUnavailable default", NewServiceUnavailableError(""),
			ErrServiceUnavailable, http.StatusServiceUnavailable, "服务暂时不可用", nil,
		},

		{
			"ImportPrerequisite", NewImportPrerequisiteError("VLM not configured"),
			ErrImportPrerequisiteMissing, http.StatusBadRequest, "VLM not configured", nil,
		},
		{
			"ModelInUse with details", NewModelInUseError("model in use", details),
			ErrModelInUse, http.StatusBadRequest, "model in use", details,
		},
		{
			"ModelInUse nil details", NewModelInUseError("model in use", nil),
			ErrModelInUse, http.StatusBadRequest, "model in use", nil,
		},

		{
			"VectorStoreBindingInvalid custom", NewVectorStoreBindingInvalidError("bad uuid"),
			ErrVectorStoreBindingInvalid, http.StatusBadRequest, "bad uuid", nil,
		},
		{
			"VectorStoreBindingInvalid default", NewVectorStoreBindingInvalidError(""),
			ErrVectorStoreBindingInvalid, http.StatusBadRequest, "vector store not found", nil,
		},
		{
			"VectorStoreUnavailable custom", NewVectorStoreUnavailableError("registry miss"),
			ErrVectorStoreUnavailable, http.StatusBadRequest, "registry miss", nil,
		},
		{
			"VectorStoreUnavailable default", NewVectorStoreUnavailableError(""),
			ErrVectorStoreUnavailable, http.StatusBadRequest, "vector store is currently unavailable", nil,
		},

		{
			"TenantNotFound", NewTenantNotFoundError(),
			ErrTenantNotFound, http.StatusNotFound, "空间不存在", nil,
		},
		{
			"TenantAlreadyExists", NewTenantAlreadyExistsError(),
			ErrTenantAlreadyExists, http.StatusConflict, "空间已存在", nil,
		},
		{
			"TenantInactive", NewTenantInactiveError(),
			ErrTenantInactive, http.StatusForbidden, "空间已停用", nil,
		},
		{
			"TenantCreationDisabled", NewTenantCreationDisabledError(),
			ErrTenantCreationDisabled, http.StatusForbidden,
			"self-service workspace creation is disabled; join a workspace by invitation", nil,
		},

		{
			"AgentMissingThinkingModel", NewAgentMissingThinkingModelError(),
			ErrAgentMissingThinkingModel, http.StatusBadRequest, "启用Agent模式前，请先选择思考模型", nil,
		},
		{
			"AgentMissingAllowedTools", NewAgentMissingAllowedToolsError(),
			ErrAgentMissingAllowedTools, http.StatusBadRequest, "至少需要选择一个允许的工具", nil,
		},
		{
			"AgentInvalidMaxIterations", NewAgentInvalidMaxIterationsError(),
			ErrAgentInvalidMaxIterations, http.StatusBadRequest,
			"最大迭代次数须为正整数，或 -1 表示不限制", nil,
		},
		{
			"AgentInvalidTemperature", NewAgentInvalidTemperatureError(),
			ErrAgentInvalidTemperature, http.StatusBadRequest, "温度参数必须在0-2之间", nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.got == nil {
				t.Fatal("constructor returned nil *AppError")
			}
			if tc.got.Code != tc.wantCode {
				t.Errorf("Code = %d, want %d", tc.got.Code, tc.wantCode)
			}
			if tc.got.HTTPCode != tc.wantHTTP {
				t.Errorf("HTTPCode = %d, want %d", tc.got.HTTPCode, tc.wantHTTP)
			}
			if tc.got.Message != tc.wantMsg {
				t.Errorf("Message = %q, want %q", tc.got.Message, tc.wantMsg)
			}
			// Details is only asserted for ModelInUse; every other
			// constructor must leave it unset.
			if tc.name == "ModelInUse with details" {
				got, ok := tc.got.Details.(map[string]any)
				if !ok || got["model"] != "m1" || got["refs"] != 2 {
					t.Errorf("Details = %#v, want %v", tc.got.Details, tc.wantDetail)
				}
			} else if tc.got.Details != nil {
				t.Errorf("Details = %#v, want nil", tc.got.Details)
			}
		})
	}
}

func TestAppErrorString(t *testing.T) {
	err := NewNotFoundError("missing")
	want := fmt.Sprintf("error code: %d, error message: %s", ErrNotFound, "missing")
	if got := err.Error(); got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}

func TestWithDetails(t *testing.T) {
	err := NewBadRequestError("bad input")
	details := map[string]string{"field": "name"}

	ret := err.WithDetails(details)
	if ret != err {
		t.Error("WithDetails should return the same *AppError pointer")
	}
	if got, ok := err.Details.(map[string]string); !ok || got["field"] != "name" {
		t.Errorf("Details = %#v, want %v", err.Details, details)
	}

	// Overwriting details replaces the previous value.
	if ret.WithDetails("plain detail") != err {
		t.Error("WithDetails should return the same *AppError pointer")
	}
	if err.Details != "plain detail" {
		t.Errorf("Details after second WithDetails = %#v, want %q", err.Details, "plain detail")
	}
}

func TestIsAppError(t *testing.T) {
	appErr := NewForbiddenError("denied")
	got, ok := IsAppError(appErr)
	if !ok {
		t.Fatal("IsAppError(*AppError) = false, want true")
	}
	if got != appErr {
		t.Error("IsAppError should return the same *AppError pointer")
	}

	if _, ok := IsAppError(stderrors.New("plain error")); ok {
		t.Error("IsAppError(plain error) = true, want false")
	}
	if _, ok := IsAppError(nil); ok {
		t.Error("IsAppError(nil) = true, want false")
	}
}

func TestSessionSentinels(t *testing.T) {
	sentinels := []struct {
		name string
		err  error
		want string
	}{
		{"ErrSessionNotFound", ErrSessionNotFound, "session not found"},
		{"ErrSessionExpired", ErrSessionExpired, "session expired"},
		{"ErrSessionLimitExceeded", ErrSessionLimitExceeded, "session limit exceeded"},
		{"ErrInvalidSessionID", ErrInvalidSessionID, "invalid session id"},
		{"ErrInvalidTenantID", ErrInvalidTenantID, "invalid tenant id"},
	}

	seen := make(map[error]bool, len(sentinels))
	for _, s := range sentinels {
		if s.err == nil {
			t.Errorf("%s is nil", s.name)
			continue
		}
		if s.err.Error() != s.want {
			t.Errorf("%s = %q, want %q", s.name, s.err.Error(), s.want)
		}
		if seen[s.err] {
			t.Errorf("%s duplicates another sentinel", s.name)
		}
		seen[s.err] = true
	}
	if len(seen) != len(sentinels) {
		t.Errorf("distinct sentinels = %d, want %d", len(seen), len(sentinels))
	}
}

func TestParseErrorCodes(t *testing.T) {
	codes := []string{
		ErrCodeDocReaderTimeout,
		ErrCodeDocReaderUnavailable,
		ErrCodeDocReaderParseFailed,
		ErrCodeChunkingFailed,
		ErrCodeEmbeddingRateLimit,
		ErrCodeEmbeddingProviderFail,
		ErrCodeVectorStoreWriteFailed,
		ErrCodeMultimodalVLMFailed,
		ErrCodeMultimodalAllFailed,
		ErrCodeTaskTimeout,
		ErrCodeTaskStalled,
		ErrCodeUnknown,
	}

	if len(codes) != 12 {
		t.Fatalf("parse error codes = %d, want 12", len(codes))
	}

	seen := make(map[string]bool, len(codes))
	for _, code := range codes {
		if code == "" {
			t.Error("parse error code is empty")
			continue
		}
		if seen[code] {
			t.Errorf("duplicate parse error code %q", code)
		}
		seen[code] = true
		for _, r := range code {
			if (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '_' {
				t.Errorf("parse error code %q is not SCREAMING_SNAKE", code)
				break
			}
		}
		if strings.ToUpper(code) != code {
			t.Errorf("parse error code %q contains lowercase characters", code)
		}
	}
}
