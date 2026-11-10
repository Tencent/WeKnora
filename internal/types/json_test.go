package types

import (
	"encoding/json"
	"testing"
)

func TestJSONUnmarshalCopiesInput(t *testing.T) {
	type wrapper struct {
		Value JSON `json:"value"`
	}

	input := []byte(`{"value":{"generated_questions":[{"id":"q1","question":"今晚吃啥"}]}}`)

	var got wrapper
	if err := json.Unmarshal(input, &got); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	// Simulate decoder buffer reuse/caller mutation after UnmarshalJSON returns.
	for i := range input {
		input[i] = 'x'
	}

	if !json.Valid(got.Value) {
		t.Fatalf("stored JSON became invalid after input mutation: %q", string(got.Value))
	}

	marshaled, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	if len(marshaled) == 0 {
		t.Fatal("expected marshaled output")
	}
}
