package v7

import (
	"strings"
	"testing"

	typesLocal "github.com/Tencent/WeKnora/internal/types"
)

func TestBaseConditionsIncludeDisabledOnlyWhenRequested(t *testing.T) {
	repository := &elasticsearchRepository{}

	defaultConditions := repository.getBaseConds(typesLocal.RetrieveParams{})
	if !strings.Contains(defaultConditions, "is_enabled") {
		t.Fatal("default conditions must exclude disabled chunks")
	}

	adminConditions := repository.getBaseConds(typesLocal.RetrieveParams{IncludeDisabled: true})
	if strings.Contains(adminConditions, "is_enabled") {
		t.Fatal("include-disabled conditions must omit the enabled filter")
	}
}
