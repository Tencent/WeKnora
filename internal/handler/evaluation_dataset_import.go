package handler

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"unicode/utf8"

	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
)

// ImportDataset godoc
// @Summary      原子导入评测数据集与初版
// @Description  request_id 在租户内幂等；不同输入复用同一标识返回 409
// @Tags         评估
// @Accept       json
// @Produce      json
// @Param        request body types.EvaluationDatasetImportInput true "数据集和初版"
// @Success      200 {object} map[string]interface{} "数据集、版本和重放状态"
// @Failure      400 {object} map[string]interface{} "输入无效"
// @Failure      409 {object} map[string]interface{} "请求标识冲突"
// @Failure      413 {object} map[string]interface{} "超出配置限制"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /evaluation/datasets/import [post]
func (h *EvaluationDatasetHandler) ImportDataset(c *gin.Context) {
	tenantID, ok := evaluationHandlerTenantID(c)
	if !ok {
		return
	}
	var request types.EvaluationDatasetImportInput
	if !h.decodeDatasetJSON(c, &request, true) {
		return
	}
	result, err := h.registryService.ImportDataset(c.Request.Context(), tenantID, &request)
	if err != nil {
		writeEvaluationDatasetError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": result})
}

func (h *EvaluationDatasetHandler) decodeDatasetJSON(c *gin.Context, target any, importing bool) bool {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, h.limits.MaxRequestBodyBytes)
	raw, err := io.ReadAll(c.Request.Body)
	var maxBytesError *http.MaxBytesError
	if errors.As(err, &maxBytesError) {
		_ = c.Error(apperrors.NewRequestEntityTooLargeError("Evaluation dataset request body exceeds the limit"))
		return false
	}
	if err == nil {
		err = validateDatasetJSON(raw, importing)
	}
	if err == nil {
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.DisallowUnknownFields()
		err = decoder.Decode(target)
	}
	if err != nil {
		_ = c.Error(apperrors.NewBadRequestError("Invalid evaluation dataset JSON").WithDetails(err.Error()))
		return false
	}
	return true
}

// Validate the wire representation before Go decoding can discard duplicate
// keys, accept case-insensitive field names, or replace null scalar values.
func validateDatasetJSON(raw []byte, importing bool) error {
	if !utf8.Valid(raw) {
		return errors.New("request must be valid UTF-8")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := walkDatasetJSON(decoder, "$", importing, 0); err != nil {
		return err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return errors.New("expected exactly one JSON value")
	}
	return nil
}

func walkDatasetJSON(decoder *json.Decoder, path string, importing bool, depth int) error {
	if depth > 64 {
		return errors.New("JSON nesting exceeds 64 levels")
	}
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	metadata := strings.Contains(path, ".passages[].metadata")
	if token == nil && !metadata {
		return fmt.Errorf("%s must not be null", path)
	}
	if value, ok := token.(string); ok && strings.ContainsRune(value, '\x00') {
		return fmt.Errorf("%s contains a NUL character", path)
	}
	switch token {
	case json.Delim('{'):
		fields := datasetJSONFields(path, importing)
		seen := map[string]bool{}
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok || seen[key] || strings.ContainsRune(key, '\x00') {
				return fmt.Errorf("%s has an invalid or duplicate field %v", path, keyToken)
			}
			if _, allowed := fields[key]; fields != nil && !allowed {
				return fmt.Errorf("%s has unknown field %q", path, key)
			}
			seen[key] = true
			if err := walkDatasetJSON(decoder, path+"."+key, importing, depth+1); err != nil {
				return err
			}
		}
		for key, required := range fields {
			if required && !seen[key] {
				return fmt.Errorf("%s requires %s", path, key)
			}
		}
		_, err = decoder.Token()
		return err
	case json.Delim('['):
		for decoder.More() {
			if err := walkDatasetJSON(decoder, path+"[]", importing, depth+1); err != nil {
				return err
			}
		}
		_, err = decoder.Token()
		return err
	}
	return nil
}

func datasetJSONFields(path string, importing bool) map[string]bool {
	if path == "$" && importing {
		return map[string]bool{"request_id": true, "name": true, "description": false, "content": true}
	}
	contentPath := "$"
	if importing {
		contentPath += ".content"
	}
	switch path {
	case contentPath:
		return map[string]bool{"passages": true, "questions": true, "relevance": true}
	case contentPath + ".passages[]":
		return map[string]bool{"pid": true, "content": true, "metadata": false}
	case contentPath + ".questions[]":
		return map[string]bool{"qid": true, "question": true, "answer": false}
	case contentPath + ".relevance[]":
		return map[string]bool{"qid": true, "pid": true, "grade": true}
	default:
		return nil
	}
}
