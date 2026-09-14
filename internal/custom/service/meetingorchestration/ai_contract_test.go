package meetingorchestration

import "testing"

func TestDecodeStrictRejectsUnknownAndTrailingContent(t *testing.T) {
	var output ItemCandidateOutput
	if err := decodeStrict(`{"candidates":[],"extra":true}`, &output); err == nil {
		t.Fatal("expected unknown field rejection")
	}
	if err := decodeStrict(`{"candidates":[]} {}`, &output); err == nil {
		t.Fatal("expected trailing content rejection")
	}
}

func TestItemCandidateValidationRejectsOutOfWhitelistEvidence(t *testing.T) {
	output := ItemCandidateOutput{Candidates: []ItemCandidate{{CoreLevel: "core", EvidenceIDs: []string{"e2"}, BusinessObject: BusinessObject{EvidenceIDs: []string{"e1"}}}}}
	if err := output.Validate(map[string]struct{}{"e1": {}}); err == nil {
		t.Fatal("expected evidence whitelist rejection")
	}
}

func TestItemCandidateValidationRequiresConditionalEvidence(t *testing.T) {
	value := "本周"
	output := ItemCandidateOutput{Candidates: []ItemCandidate{{CoreLevel: "core", EvidenceIDs: []string{"e1"}, BusinessObject: BusinessObject{EvidenceIDs: []string{"e1"}}, CycleSignal: &value}}}
	if err := output.Validate(map[string]struct{}{"e1": {}}); err == nil {
		t.Fatal("expected cycle evidence rejection")
	}
}

func TestTopicAndWorkItemMatchesRequireExactlyOneResult(t *testing.T) {
	allowed := map[string]struct{}{"e1": {}}
	if err := (TopicMatchOutput{}).Validate(allowed); err == nil {
		t.Fatal("expected empty topic match rejection")
	}
	if err := (WorkItemMatchOutput{}).Validate(allowed); err == nil {
		t.Fatal("expected empty work item match rejection")
	}
}
