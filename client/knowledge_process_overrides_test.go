package client

import (
	"encoding/json"
	"testing"
)

func decodeOverrides(t *testing.T, overrides KnowledgeProcessOverrides) map[string]json.RawMessage {
	t.Helper()
	data, err := json.Marshal(overrides)
	if err != nil {
		t.Fatalf("marshal overrides: %v", err)
	}
	var decoded map[string]json.RawMessage
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("decode overrides: %v", err)
	}
	return decoded
}

func decodeObject(t *testing.T, raw json.RawMessage) map[string]json.RawMessage {
	t.Helper()
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		t.Fatalf("decode object: %v", err)
	}
	return object
}

// A per-upload chunking override is merged into the knowledge base config, so
// an unset field must stay absent. Sending chunk_overlap: 0 would now mean
// "disable the inherited overlap" (#6100) and token_limit: 0 would disable the
// inherited budget.
func TestProcessOverrideChunkingPreservesUnsetFields(t *testing.T) {
	chunking := decodeObject(t, decodeOverrides(t, KnowledgeProcessOverrides{
		ChunkingConfig: &ChunkingConfig{ChunkSize: 512},
	})["chunking_config"])

	if string(chunking["chunk_size"]) != "512" {
		t.Fatalf("chunk_size = %s, want 512", chunking["chunk_size"])
	}
	for _, field := range []string{"chunk_overlap", "token_limit"} {
		if _, present := chunking[field]; present {
			t.Fatalf("%s was sent for an unset value: %s", field, chunking[field])
		}
	}

	chunking = decodeObject(t, decodeOverrides(t, KnowledgeProcessOverrides{
		ChunkingConfig: &ChunkingConfig{ChunkSize: 512, ChunkOverlap: 32, TokenLimit: 128},
	})["chunking_config"])
	if string(chunking["chunk_overlap"]) != "32" || string(chunking["token_limit"]) != "128" {
		t.Fatalf("explicit values lost: %v", chunking)
	}
}

// The server's question-generation config carries the model binding and the
// custom instructions; the SDK request must not drop them on upload/reparse.
func TestProcessOverrideQuestionGenerationRetainsModelAndInstructions(t *testing.T) {
	question := decodeObject(t, decodeOverrides(t, KnowledgeProcessOverrides{
		QuestionGenerationConfig: &QuestionGenerationConfig{
			ModelID: "model-1", Enabled: true, QuestionCount: 5,
			CustomInstructions: "for new hires",
		},
	})["question_generation_config"])

	if string(question["model_id"]) != `"model-1"` {
		t.Fatalf("model_id = %s", question["model_id"])
	}
	if string(question["question_count"]) != "5" {
		t.Fatalf("question_count = %s", question["question_count"])
	}
	if string(question["custom_instructions"]) != `"for new hires"` {
		t.Fatalf("custom_instructions = %s", question["custom_instructions"])
	}
	if string(question["enabled"]) != "true" {
		t.Fatalf("enabled = %s", question["enabled"])
	}
}
