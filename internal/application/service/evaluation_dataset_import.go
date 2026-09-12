package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/google/uuid"
)

// ImportDataset validates the complete request before the atomic repository call.
// The tenant and request UUID determine identity; the immutable manifest pins
// metadata and array ordering independently of the version-1 content hash.
func (s *EvaluationDatasetRegistryService) ImportDataset(
	ctx context.Context, tenantID uint64, input *types.EvaluationDatasetImportInput,
) (*types.EvaluationDatasetImportResult, error) {
	if input == nil || tenantID == 0 || input.Content == nil {
		return nil, fmt.Errorf("tenant and import content are required: %w", interfaces.ErrEvaluationDatasetInvalid)
	}
	requestID, err := uuid.Parse(input.RequestID)
	if err != nil || requestID == uuid.Nil || input.RequestID != requestID.String() {
		return nil, fmt.Errorf("request_id must be a canonical nonzero UUID: %w",
			interfaces.ErrEvaluationDatasetInvalid)
	}
	name := strings.TrimSpace(input.Name)
	if name == "" || utf8.RuneCountInString(name) > 255 || !validEvaluationDatasetText(name) ||
		!validEvaluationDatasetText(input.Description) {
		return nil, fmt.Errorf("invalid dataset name or description: %w", interfaces.ErrEvaluationDatasetInvalid)
	}
	if len(input.Description) > s.limits.MaxQuestionBytes {
		return nil, fmt.Errorf("description exceeds max_question_bytes: %w",
			interfaces.ErrEvaluationDatasetLimitExceeded)
	}
	if err := s.validateLimits(input.Content); err != nil {
		return nil, err
	}
	if len(input.Content.Passages) == 0 || len(input.Content.Questions) == 0 || input.Content.Relevance == nil {
		return nil, fmt.Errorf("passages and questions must be nonempty; relevance must be an array: %w",
			interfaces.ErrEvaluationDatasetInvalid)
	}
	if err := validateEvaluationDatasetFields(input.Content); err != nil {
		return nil, err
	}
	requestBytes, err := json.Marshal(input)
	if err != nil {
		return nil, fmt.Errorf("invalid import JSON: %w", interfaces.ErrEvaluationDatasetInvalid)
	}
	if int64(len(requestBytes)) > s.limits.MaxRequestBodyBytes {
		return nil, fmt.Errorf("import exceeds max_request_body_bytes: %w",
			interfaces.ErrEvaluationDatasetLimitExceeded)
	}
	datasetID := uuid.NewSHA1(uuid.NameSpaceURL,
		[]byte("weknora/evaluation/import/v1/"+strconv.FormatUint(tenantID, 10)+"/"+requestID.String())).String()
	manifest, err := json.Marshal(map[string]string{
		"source": "api_import", "request_id": requestID.String(),
		"request_sha256": types.EvaluationDatasetArtifactSHA256(requestBytes),
	})
	if err != nil {
		return nil, err
	}
	dataset := &types.EvaluationDataset{
		ID: datasetID, Scope: types.EvaluationDatasetScopeTenant, OwnerTenantID: &tenantID,
		Name: name, Description: input.Description,
	}
	version := &types.EvaluationDatasetVersion{
		ID:        uuid.NewSHA1(uuid.MustParse(datasetID), []byte("initial-version")).String(),
		DatasetID: datasetID, VersionNumber: 1, SchemaVersion: types.EvaluationDatasetSchemaVersion,
		ArtifactSHA256: types.EvaluationDatasetArtifactSHA256(canonicalEvaluationDatasetPayload(input.Content)),
		ContentSHA256:  types.CanonicalEvaluationDatasetContentSHA256(input.Content), Manifest: types.JSON(manifest),
		PassageCount: len(input.Content.Passages), QuestionCount: len(input.Content.Questions),
		RelevanceCount: len(input.Content.Relevance),
	}
	return s.repo.ImportDataset(ctx, dataset, version, input.Content)
}

func validEvaluationDatasetText(value string) bool {
	return utf8.ValidString(value) && !strings.ContainsRune(value, '\x00')
}

// Validate fields before hashing raw metadata or writing to either SQL dialect.
func validateEvaluationDatasetFields(content *types.EvaluationDatasetVersionInput) error {
	invalid := func(field string) error {
		return fmt.Errorf("invalid %s: %w", field, interfaces.ErrEvaluationDatasetInvalid)
	}
	validID := func(id string) bool {
		return strings.TrimSpace(id) != "" && utf8.RuneCountInString(id) <= 128 && validEvaluationDatasetText(id)
	}
	for _, passage := range content.Passages {
		if !validID(passage.PID) || strings.TrimSpace(passage.Content) == "" ||
			!validEvaluationDatasetText(passage.Content) {
			return invalid("passage id or content")
		}
		if len(passage.Metadata) > 0 {
			var metadata map[string]json.RawMessage
			if json.Unmarshal(passage.Metadata, &metadata) != nil || metadata == nil ||
				!validEvaluationDatasetMetadata(passage.Metadata) {
				return invalid("passage metadata object")
			}
		}
	}
	for _, question := range content.Questions {
		if !validID(question.QID) || strings.TrimSpace(question.Question) == "" ||
			!validEvaluationDatasetText(question.Question) || !validEvaluationDatasetText(question.Answer) {
			return invalid("question id, question or answer")
		}
	}
	for _, edge := range content.Relevance {
		if !validID(edge.QID) || !validID(edge.PID) || edge.Grade < 0 || int64(edge.Grade) > 2147483647 {
			return invalid("relevance id or grade (expected integer 0..2147483647)")
		}
	}
	return nil
}

func validEvaluationDatasetMetadata(raw []byte) bool {
	if !utf8.Valid(raw) {
		return false
	}
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.UseNumber()
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			return true
		}
		if err != nil {
			return false
		}
		if value, ok := token.(string); ok && !validEvaluationDatasetText(value) {
			return false
		}
	}
}
