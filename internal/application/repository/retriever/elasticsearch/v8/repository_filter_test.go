package v8

import (
	"encoding/json"
	"strings"
	"testing"

	typesLocal "github.com/Tencent/WeKnora/internal/types"
)

func TestBaseConditionsIncludeDisabledOnlyWhenRequested(t *testing.T) {
	repository := &elasticsearchRepository{}

	defaultConditions, err := json.Marshal(repository.getBaseConds(typesLocal.RetrieveParams{}))
	if err != nil {
		t.Fatalf("marshal default conditions: %v", err)
	}
	if !strings.Contains(string(defaultConditions), "is_enabled") {
		t.Fatal("default conditions must exclude disabled chunks")
	}

	adminConditions, err := json.Marshal(repository.getBaseConds(typesLocal.RetrieveParams{IncludeDisabled: true}))
	if err != nil {
		t.Fatalf("marshal include-disabled conditions: %v", err)
	}
	if strings.Contains(string(adminConditions), "is_enabled") {
		t.Fatal("include-disabled conditions must omit the enabled filter")
	}
}
