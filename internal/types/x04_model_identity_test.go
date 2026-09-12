package types

import "testing"

func TestX04ModelIdentityVersionsAndLimits(t *testing.T) {
	model := &Model{ID: "fixture"}
	old := EvaluationModelConfigSHA256ForVersion(model, 1)
	initial := EvaluationModelConfigSHA256(model)
	model.Parameters.ContextWindow = 10000
	if EvaluationModelConfigSHA256(model) == initial {
		t.Fatal("context window absent from identity")
	}
	second := EvaluationModelConfigSHA256(model)
	model.Parameters.MaxOutputTokens = 300
	if EvaluationModelConfigSHA256(model) == second {
		t.Fatal("output ceiling absent from identity")
	}
	if EvaluationModelConfigSHA256ForVersion(model, 1) != old {
		t.Fatal("frozen v1 interpretation changed")
	}
	if EvaluationModelSnapshotFrom(model).ConfigVersion != 2 {
		t.Fatal("new snapshot must declare v2")
	}
}

func TestX04AggregateReportsKnownAndUnknownCalls(t *testing.T) {
	var usage TokenUsage
	usage.Accumulate(TokenUsage{UsageReported: true})
	usage.Accumulate(TokenUsage{})
	if usage.UsageReported || usage.UsageReportedCalls != 1 || usage.UsageUnreportedCalls != 1 {
		t.Fatalf("aggregate completeness = %+v", usage)
	}
}

func TestX04ModelIdentityIncludesProviderModelAndRole(t *testing.T) {
	model := &Model{ID: "fixture"}
	legacy := EvaluationModelConfigSHA256ForVersion(model, 1)
	changes := []func(){
		func() { model.Name = "provider-model-v2" },
		func() { model.Type = ModelTypeKnowledgeQA },
		func() { model.Source = ModelSourceRemote },
	}
	for i, change := range changes {
		before := EvaluationModelConfigSHA256(model)
		change()
		if EvaluationModelConfigSHA256(model) == before {
			t.Fatalf("model identity change %d absent from v2 fingerprint", i)
		}
		if EvaluationModelConfigSHA256ForVersion(model, 1) != legacy {
			t.Fatal("frozen v1 interpretation changed")
		}
	}
}
