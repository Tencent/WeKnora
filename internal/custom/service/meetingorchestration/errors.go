package meetingorchestration

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// GenerationError carries a stable, user-safe category while retaining the
// wrapped cause for logs and tests. Persisted job messages must use only Code.
type GenerationError struct {
	Code string
	Err  error
}

func (e *GenerationError) Error() string {
	if e == nil {
		return ""
	}
	if e.Err == nil {
		return e.Code
	}
	return e.Code + ": " + e.Err.Error()
}

func (e *GenerationError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func generationError(code string, err error) error {
	if err == nil {
		return nil
	}
	return &GenerationError{Code: code, Err: err}
}

func classifyMeetingTransportError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return generationError("model_timeout", err)
	}
	if errors.Is(err, context.Canceled) {
		return err
	}
	var temporary interface{ Temporary() bool }
	if errors.As(err, &temporary) && temporary.Temporary() {
		return generationError("model_transport_failed", err)
	}
	return generationError("model_transport_failed", err)
}

func classifyMeetingValidationError(err error) error {
	if err == nil {
		return nil
	}
	message := strings.ToLower(err.Error())
	switch {
	case strings.Contains(message, "trailing content"):
		return generationError("model_output_trailing_content", err)
	case strings.Contains(message, "invalid meeting model json"), strings.Contains(message, "invalid json"):
		return generationError("model_output_invalid_json", err)
	default:
		return generationError("model_output_contract_invalid", err)
	}
}

func correctionSummary(err error) string {
	if err == nil {
		return "输出未通过结构校验"
	}
	message := strings.TrimSpace(err.Error())
	if len(message) > 2000 {
		message = message[:2000]
	}
	return fmt.Sprintf("结构校验失败：%s", message)
}

func isCorrectableMeetingValidationError(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	for _, marker := range []string{"whitelist", "identity mismatch", "unknown topic", "unknown item", "wrong topic side", "out of scope", "references unknown"} {
		if strings.Contains(message, marker) {
			return false
		}
	}
	return true
}

func meetingErrorCode(defaultCode string, err error) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return "model_timeout"
	}
	var generationErr *GenerationError
	if errors.As(err, &generationErr) && strings.TrimSpace(generationErr.Code) != "" {
		return generationErr.Code
	}
	message := strings.ToLower(fmt.Sprint(err))
	for _, marker := range []string{"whitelist", "unknown topic", "unknown item", "identity mismatch", "wrong topic side", "out of scope"} {
		if strings.Contains(message, marker) {
			return "model_reference_out_of_scope"
		}
	}
	return defaultCode
}

func safeMeetingErrorMessage(code string) string {
	switch code {
	case "model_output_invalid_json":
		return "模型输出不是合法 JSON，请重新生成"
	case "model_output_unknown_field":
		return "模型输出包含未约定字段，请重新生成"
	case "model_output_trailing_content":
		return "模型输出包含多余内容，请重新生成"
	case "model_output_contract_invalid":
		return "模型输出未满足会议字段契约，请重新生成"
	case "model_correction_failed":
		return "模型输出纠偏失败，未更新当前结果"
	case "model_timeout":
		return "模型处理超时，未更新当前结果"
	case "model_transport_failed":
		return "模型服务暂时不可用，未更新当前结果"
	case "model_reference_out_of_scope":
		return "模型引用超出当前会议范围，未更新当前结果"
	case "input_insufficient_evidence":
		return "当前会议缺少可核验的总结或原文证据，未更新当前结果"
	case "model_not_configured":
		return "会议分析模型尚未配置，未更新当前结果"
	default:
		return "会议主题簇生成失败，当前结果未改变"
	}
}

func safeMeetingTransportReason(err error) string {
	if err == nil {
		return "none"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "deadline_exceeded"
	}
	if errors.Is(err, context.Canceled) {
		return "canceled"
	}
	var streamTimeout interface{ StreamTimeoutPhase() string }
	if errors.As(err, &streamTimeout) {
		return "stream_" + streamTimeout.StreamTimeoutPhase() + "_timeout"
	}
	message := strings.ToLower(err.Error())
	for _, status := range []string{"400", "401", "403", "408", "409", "422", "429", "500", "502", "503", "504"} {
		if strings.Contains(message, "status "+status) {
			return "http_status_" + status
		}
	}
	var closed interface{ ConnectionClosed() bool }
	if errors.As(err, &closed) && closed.ConnectionClosed() {
		return "connection_closed"
	}
	var temporary interface{ Temporary() bool }
	if errors.As(err, &temporary) && temporary.Temporary() {
		return "temporary_transport_error"
	}
	return "transport_error"
}

func safeMeetingValidationReason(err error) string {
	if err == nil {
		return "none"
	}
	message := strings.ToLower(err.Error())
	switch {
	case strings.Contains(message, "trailing content"):
		return "trailing_content"
	case strings.Contains(message, "invalid meeting model json"), strings.Contains(message, "invalid json"):
		return "invalid_json"
	case strings.Contains(message, "whitelist"), strings.Contains(message, "out of scope"):
		return "reference_out_of_scope"
	case strings.Contains(message, "identity mismatch"), strings.Contains(message, "unknown topic"), strings.Contains(message, "unknown item"):
		return "identity_out_of_scope"
	case strings.Contains(message, "schema"):
		return "schema_contract_invalid"
	default:
		return "business_contract_invalid"
	}
}
