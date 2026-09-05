package knowledge

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

// PublishGateStatus is the result of the P3-04 pre-Wiki gate.
type PublishGateStatus string

const (
	PublishGatePassed   PublishGateStatus = "passed"
	PublishGateRejected PublishGateStatus = "rejected"
)

// PublishGateResult keeps either the publishable object or its rejection
// record. It contains no Wiki or Graph identifiers and is safe to persist as
// the P3-04 audit artifact.
type PublishGateResult struct {
	CandidateID string               `json:"candidate_id"`
	Status      PublishGateStatus    `json:"status"`
	Object      *ClassifiedKnowledge `json:"object,omitempty"`
	Rejected    *RejectedProposition `json:"rejected,omitempty"`
	Reason      string               `json:"reason,omitempty"`
}

// PublishGateBatch is the batch form consumed by a later Wiki writer. Only
// Passed may be sent to that writer; Rejected is an audit-only list.
type PublishGateBatch struct {
	Passed   []ClassifiedKnowledge `json:"passed"`
	Rejected []RejectedProposition `json:"rejected"`
}

// ValidateMinimumStructure applies the P3-04 minimum structure gate. The
// allowed field set comes from the existing type framework mapping. Unknown
// fields do not contribute to the minimum, but are left intact for later
// contract stages to report without silently changing source data.
func ValidateMinimumStructure(object ClassifiedKnowledge) error {
	if object.PrimaryType == TypeEntity && !IsEntitySubType(strings.TrimSpace(object.EntitySubType)) {
		return fmt.Errorf("entity_sub_type must be one of the supported entity subtypes")
	}
	keys := frameworkKeys(object.PrimaryType, strings.TrimSpace(object.EntitySubType))
	if len(keys) == 0 {
		return fmt.Errorf("no structure framework exists for primary_type %q", object.PrimaryType)
	}
	allowed := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		allowed[key] = struct{}{}
	}
	filled := 0
	for key, value := range object.StructureFields {
		if _, ok := allowed[strings.ToLower(strings.TrimSpace(key))]; ok && strings.TrimSpace(value) != "" {
			filled++
		}
	}
	if filled < 2 {
		return fmt.Errorf("%s requires at least 2 valid structure fields (got %d)", object.PrimaryType, filled)
	}
	return nil
}

// ValidatePublishGate checks all P3-04 conditions without performing any
// external write. Confidence is deliberately checked for finite values as
// JSON cannot represent NaN or infinity reliably.
func ValidatePublishGate(object ClassifiedKnowledge) error {
	if err := object.Validate(); err != nil {
		return err
	}
	if math.IsNaN(object.ClassificationConfidence) || math.IsInf(object.ClassificationConfidence, 0) {
		return fmt.Errorf("classification_confidence must be finite")
	}
	return ValidateMinimumStructure(object)
}

// GateClassifiedKnowledge returns a deterministic decision. Invalid or
// under-specified objects are represented as rejected records instead of
// being returned as a caller error, so a batch can continue auditing the
// remaining objects.
func GateClassifiedKnowledge(object ClassifiedKnowledge) PublishGateResult {
	object = cloneClassifiedKnowledge(object)
	result := PublishGateResult{CandidateID: object.CandidateID}
	if err := ValidatePublishGate(object); err != nil {
		result.Status = PublishGateRejected
		result.Reason = err.Error()
		result.Rejected = &RejectedProposition{
			CandidateID: object.CandidateID,
			Title:       strings.TrimSpace(object.Title),
			PrimaryType: object.PrimaryType,
			Reason:      result.Reason,
			EvidenceIDs: sortedEvidenceIDs(object.EvidenceIDs),
		}
		return result
	}
	object.AuditStatus = "passed"
	result.Status = PublishGatePassed
	result.Object = &object
	return result
}

// ApplyPublishGate evaluates all objects and separates the only objects that
// may reach a Wiki writer from audit-only rejection records.
func ApplyPublishGate(objects []ClassifiedKnowledge) PublishGateBatch {
	result := PublishGateBatch{
		Passed:   make([]ClassifiedKnowledge, 0, len(objects)),
		Rejected: make([]RejectedProposition, 0),
	}
	for _, object := range objects {
		decision := GateClassifiedKnowledge(object)
		if decision.Status == PublishGatePassed && decision.Object != nil {
			result.Passed = append(result.Passed, *decision.Object)
		} else if decision.Rejected != nil {
			result.Rejected = append(result.Rejected, *decision.Rejected)
		}
	}
	sort.SliceStable(result.Passed, func(i, j int) bool { return result.Passed[i].CandidateID < result.Passed[j].CandidateID })
	sort.SliceStable(result.Rejected, func(i, j int) bool { return result.Rejected[i].CandidateID < result.Rejected[j].CandidateID })
	return result
}

// PublishGate is a concise pipeline alias retained for orchestration code.
func PublishGate(objects []ClassifiedKnowledge) PublishGateBatch { return ApplyPublishGate(objects) }

// GateSplitResult applies the gate to P3-03 objects and carries forward any
// propositions that P3-03 already rejected (for example ordinary word
// meanings). No rejected proposition is promoted to the publish queue.
func GateSplitResult(split SplitResult) PublishGateBatch {
	result := ApplyPublishGate(split.Objects)
	result.Rejected = append(result.Rejected, split.Rejected...)
	sort.SliceStable(result.Rejected, func(i, j int) bool { return result.Rejected[i].CandidateID < result.Rejected[j].CandidateID })
	return result
}

// ValidateAndGate is an orchestration-friendly alias for GateClassifiedKnowledge.
func ValidateAndGate(object ClassifiedKnowledge) PublishGateResult {
	return GateClassifiedKnowledge(object)
}
