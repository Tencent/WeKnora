package service

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	werrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/models/vlm"
	"github.com/Tencent/WeKnora/internal/types"
)

// scannedPDFRequirement treats malformed scan metadata as a parser failure.
// A native text page in a mixed PDF cannot waive its scanned pages.
func scannedPDFRequirement(result *types.ReadResult) (bool, error) {
	if result == nil {
		return false, errors.New("document parser returned no result")
	}
	raw, present := result.Metadata["scanned_page_count"]
	source := strings.ToLower(strings.TrimSpace(result.Metadata["image_source_type"]))
	hasScanRef := false
	for _, ref := range result.ImageRefs {
		name := path.Base(strings.ReplaceAll(ref.Filename, "\\", "/"))
		if strings.HasPrefix(strings.ToLower(name), "scan_page_") {
			hasScanRef = true
		}
	}
	parseCount := func(raw string) (int, error) {
		if raw == "" || strings.IndexFunc(raw, func(r rune) bool { return r < '0' || r > '9' }) >= 0 {
			return 0, errors.New("invalid scanned PDF count metadata")
		}
		count, err := strconv.Atoi(raw)
		if err != nil {
			return 0, errors.New("invalid scanned PDF count metadata")
		}
		return count, nil
	}
	if present {
		count, err := parseCount(raw)
		if err != nil {
			return false, err
		}
		if (source == "scanned_pdf" || hasScanRef) && count == 0 {
			return false, errors.New("inconsistent scanned PDF metadata")
		}
		result.Metadata["scanned_page_count_source"] = "explicit_scanned_page_count"
		return count > 0, nil
	}
	if source == "scanned_pdf" {
		// Legacy PDFScannedParser emits page_count (all pages are scanned).
		// Explicit scanned_page_count above always wins; never fallback from 0.
		count, err := parseCount(result.Metadata["page_count"])
		if err != nil || count == 0 {
			return false, errors.New("scanned PDF missing valid page_count metadata")
		}
		result.Metadata["scanned_page_count_source"] = "derived_page_count"
		return true, nil
	}
	if hasScanRef {
		return false, errors.New("scanned PDF missing scanned_page_count metadata")
	}

	return false, nil
}

// guardScannedPDFOCR is a configuration/capability guard, not a model readiness
// probe. Actual OCR still runs through the existing image multimodal worker.
func (s *knowledgeService) guardScannedPDFOCR(ctx context.Context, payload types.DocumentProcessPayload, knowledge *types.Knowledge, eff types.EffectiveProcessConfig, result *types.ReadResult) error {
	if result != nil && result.Error != "" {
		return nil
	}
	required, cause := scannedPDFRequirement(result)
	reason := ""
	if cause != nil {
		reason = "Document parser returned invalid scanned PDF metadata"
	}
	if cause == nil && required {
		switch {
		case !payload.EnableMultimodel || !eff.EnableMultimodel || !eff.VLMConfig.IsEnabled():
			reason = "Scanned PDF pages require configured OCR/VLM; image placeholders are not complete text ingestion"
		case s.imageResolver == nil || s.task == nil:
			reason = "Scanned PDF OCR/VLM image processing pipeline is unavailable"
		default:
			var model vlm.VLM
			if eff.VLMConfig.ModelID != "" {
				if s.modelService == nil {
					cause = errors.New("model service unavailable")
				} else {
					model, cause = s.modelService.GetVLMModel(ctx, eff.VLMConfig.ModelID)
				}
			} else {
				// Legacy inline configuration is resolved by the existing image
				// worker (which owns its Ollama dependency). Do not add a new
				// dependency or select a fallback provider here.
				endpoint, parseErr := url.Parse(eff.VLMConfig.BaseURL)
				if parseErr != nil || endpoint.Host == "" || (endpoint.Scheme != "http" && endpoint.Scheme != "https") {
					cause = errors.New("invalid legacy VLM endpoint")
				}
				interfaceType := strings.ToLower(eff.VLMConfig.InterfaceType)
				if interfaceType != "" && interfaceType != "openai" && interfaceType != "ollama" {
					cause = errors.New("invalid legacy VLM interface")
				}
			}
			if cause != nil || (eff.VLMConfig.ModelID != "" && model == nil) {
				reason = "Scanned PDF OCR/VLM configuration cannot be resolved"
			}
		}
	}
	if reason == "" {
		return nil
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if aborted, status := s.isKnowledgeAborted(ctx, knowledge.TenantID, knowledge.ID); aborted {
		return abortRetryErr(ctx, knowledge.ID, status)
	}
	if s.isKnowledgeSourceReplaced(ctx, knowledge) || s.currentAttemptSuperseded(ctx, knowledge.ID) {
		return errors.New("scanned PDF guard belongs to a superseded source or parse attempt")
	}
	knowledge.ParseStatus = types.ParseStatusFailed
	knowledge.ErrorMessage = reason
	knowledge.UpdatedAt = time.Now()
	if err := s.updateKnowledgeUnlessSourceReplaced(ctx, knowledge); err != nil {
		return fmt.Errorf("persist scanned PDF capability failure: %w", err)
	}
	s.failStage(ctx, knowledge.ID, types.StageDocReader, werrors.ErrCodeDocReaderParseFailed, reason, cause)
	return errors.New(reason)
}
