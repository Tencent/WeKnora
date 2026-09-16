package types

import (
	"maps"
	"net/http"
	"strings"

	"github.com/Tencent/WeKnora/internal/utils"
	"github.com/google/uuid"
)

// EvaluationModelBehaviorRevisionKey identifies the non-secret revision that
// makes effective custom-header routing changes visible to model fingerprints
// without persisting header names or values in those fingerprints.
const EvaluationModelBehaviorRevisionKey = "behavior_revision"

// MaintainModelBehaviorRevision updates the revision at the model write
// boundary. Header names are compared with the same normalization and reserved
// header rules used by outbound requests, so representation-only edits do not
// invalidate model identity.
func MaintainModelBehaviorRevision(previous *ModelParameters, next *ModelParameters) {
	nextHeaders := effectiveModelCustomHeaders(next.CustomHeaders)
	if previous == nil {
		if len(nextHeaders) > 0 {
			setModelBehaviorRevision(next, uuid.NewString())
		}
		return
	}

	previousHeaders := effectiveModelCustomHeaders(previous.CustomHeaders)
	if !maps.Equal(previousHeaders, nextHeaders) {
		setModelBehaviorRevision(next, uuid.NewString())
		return
	}

	previousRevision := previous.ExtraConfig[EvaluationModelBehaviorRevisionKey]
	if next.ExtraConfig[EvaluationModelBehaviorRevisionKey] == "" && previousRevision != "" {
		setModelBehaviorRevision(next, previousRevision)
		return
	}
	if len(nextHeaders) > 0 && next.ExtraConfig[EvaluationModelBehaviorRevisionKey] == "" {
		setModelBehaviorRevision(next, uuid.NewString())
	}
}

func effectiveModelCustomHeaders(headers map[string]string) map[string]string {
	effective := make(map[string]string, len(headers))
	for rawName, value := range headers {
		name := strings.TrimSpace(rawName)
		if name == "" || utils.IsReservedHeader(name) {
			continue
		}
		effective[http.CanonicalHeaderKey(name)] = value
	}
	return effective
}

func setModelBehaviorRevision(parameters *ModelParameters, revision string) {
	if parameters.ExtraConfig == nil {
		parameters.ExtraConfig = make(map[string]string)
	}
	parameters.ExtraConfig[EvaluationModelBehaviorRevisionKey] = revision
}
