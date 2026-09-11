package trainingorchestration

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"

	"github.com/Tencent/WeKnora/internal/custom/service/transcript"
)

const (
	defaultRetrievalCriteriaPerQuery = 5
	defaultRetrievedEvidencePerVideo = 5
)

// EvidenceQueryReader searches only within the immutable evidence whitelist
// selected by planning. The returned text is not trusted as source material;
// search only selects IDs from the already validated material package.
type EvidenceQueryReader interface {
	SearchEvidence(context.Context, string, string, string, []string, int) ([]transcript.Chunk, error)
}

// QueryMaterialRetriever narrows a complete material package after planning.
// It batches planned inclusion criteria into retrieval queries and keeps at
// least one validated evidence sentence for every source video.
type QueryMaterialRetriever struct {
	Evidence            EvidenceQueryReader
	MaxCriteriaPerQuery int
	MaxEvidencePerVideo int
}

func (r *QueryMaterialRetriever) Retrieve(ctx context.Context, cluster PlanCluster, material ClusterMaterial) (ClusterMaterial, error) {
	if r == nil || r.Evidence == nil {
		return ClusterMaterial{}, fmt.Errorf("training orchestration material retriever is not configured")
	}
	if err := material.ValidateAgainst(cluster); err != nil {
		return ClusterMaterial{}, fmt.Errorf("validate complete material before retrieval: %w", err)
	}
	criteriaLimit := r.MaxCriteriaPerQuery
	if criteriaLimit <= 0 {
		criteriaLimit = defaultRetrievalCriteriaPerQuery
	}
	evidenceLimit := r.MaxEvidencePerVideo
	if evidenceLimit <= 0 {
		evidenceLimit = defaultRetrievedEvidencePerVideo
	}
	queries := buildMaterialRetrievalQueries(cluster, criteriaLimit)
	evidenceByKey := make(map[string]MaterialEvidence, len(material.Evidence))
	for _, item := range material.Evidence {
		evidenceByKey[item.VideoID+"\x00"+item.EvidenceID] = item
	}

	selected := make([]MaterialEvidence, 0, len(cluster.MaterialRequests)*evidenceLimit)
	for _, request := range cluster.MaterialRequests {
		allowed := make(map[string]struct{}, len(request.EvidenceIDs))
		for _, evidenceID := range request.EvidenceIDs {
			allowed[strings.TrimSpace(evidenceID)] = struct{}{}
		}
		seen := make(map[string]struct{}, evidenceLimit)
		for _, query := range queries {
			remaining := evidenceLimit - len(seen)
			if remaining <= 0 {
				break
			}
			matches, err := r.Evidence.SearchEvidence(ctx, request.VideoID, request.TranscriptGeneration, query, request.EvidenceIDs, remaining)
			if err != nil {
				if isRetrievalScopeViolation(err) {
					return ClusterMaterial{}, &GenerationError{Code: "invalid_reference", Err: fmt.Errorf("retrieved evidence is outside the planned evidence whitelist: %w", err)}
				}
				if reason, ok := retrievalDegradationReason(ctx, err); ok {
					return degradedMaterial(material, reason), nil
				}
				return ClusterMaterial{}, &GenerationError{Code: "evidence_retrieval_failed", Err: fmt.Errorf("retrieve planned evidence: %w", err)}
			}
			for _, match := range matches {
				evidenceID := strings.TrimSpace(match.EvidenceSentenceID)
				if _, ok := allowed[evidenceID]; !ok {
					return ClusterMaterial{}, &GenerationError{Code: "invalid_reference", Err: fmt.Errorf("retrieved evidence is outside the planned evidence whitelist")}
				}
				key := strings.TrimSpace(request.VideoID) + "\x00" + evidenceID
				item, ok := evidenceByKey[key]
				if !ok {
					return ClusterMaterial{}, &GenerationError{Code: "invalid_reference", Err: fmt.Errorf("retrieved evidence is outside the validated material")}
				}
				if _, duplicate := seen[evidenceID]; duplicate {
					continue
				}
				seen[evidenceID] = struct{}{}
				selected = append(selected, item)
				if len(seen) >= evidenceLimit {
					break
				}
			}
		}
		if len(seen) == 0 {
			// Search is only an optional narrowing step. An empty result does
			// not invalidate the already materialized, fully validated plan.
			return degradedMaterial(material, "empty_result"), nil
		}
	}
	result := material
	result.Evidence = selected
	if err := result.ValidateRetrievedAgainst(cluster); err != nil {
		return ClusterMaterial{}, fmt.Errorf("validate retrieved material: %w", err)
	}
	return result, nil
}

func isRetrievalScopeViolation(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	for _, marker := range []string{
		"outside video", "outside the planned evidence", "outside the evidence",
		"outside the whitelist", "outside whitelist",
	} {
		if strings.Contains(message, marker) {
			return true
		}
	}
	return false
}

func degradedMaterial(material ClusterMaterial, reason string) ClusterMaterial {
	material.RetrievalDegraded = true
	material.RetrievalDegradationReason = strings.TrimSpace(reason)
	if material.RetrievalDegradationReason == "" {
		material.RetrievalDegradationReason = "search_unavailable"
	}
	return material
}

// retrievalDegradationReason recognizes only failures that are safe to treat
// as an unavailable optional search accelerator. Validation and source-data
// errors deliberately remain hard failures.
func retrievalDegradationReason(ctx context.Context, err error) (string, bool) {
	if err == nil || ctx == nil {
		return "", false
	}
	// If the orchestration task itself is cancelled or expired, generation
	// cannot safely continue on the same context.
	if ctx.Err() != nil {
		return "", false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout", true
	}
	var networkErr net.Error
	if errors.As(err, &networkErr) && (networkErr.Timeout() || networkErr.Temporary()) {
		return "network_error", true
	}
	message := strings.ToLower(err.Error())
	for _, marker := range []string{
		" status 429", " status 502", " status 503", " status 504",
		"temporarily unavailable", "service unavailable", "search unavailable",
		"connection refused", "connection reset", "network is unreachable",
		"timeout", "deadline exceeded",
	} {
		if strings.Contains(message, marker) {
			return "search_unavailable", true
		}
	}
	return "", false
}

func buildMaterialRetrievalQueries(cluster PlanCluster, maxCriteria int) []string {
	baseParts := []string{strings.TrimSpace(cluster.Title), strings.TrimSpace(cluster.LearningGoal), strings.TrimSpace(cluster.Scope)}
	base := strings.Join(nonEmptyStrings(baseParts), "；")
	criteria := nonEmptyStrings(cluster.InclusionCriteria)
	if len(criteria) == 0 {
		return []string{base}
	}
	queries := make([]string, 0, (len(criteria)+maxCriteria-1)/maxCriteria)
	for start := 0; start < len(criteria); start += maxCriteria {
		end := start + maxCriteria
		if end > len(criteria) {
			end = len(criteria)
		}
		parts := append([]string{base}, criteria[start:end]...)
		queries = append(queries, strings.Join(nonEmptyStrings(parts), "；"))
	}
	return queries
}

func nonEmptyStrings(values []string) []string {
	result := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, duplicate := seen[value]; duplicate {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}
