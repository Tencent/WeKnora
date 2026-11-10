package rerank

import (
	"encoding/json"
	"testing"
)

func TestRankResultUnmarshalJSON(t *testing.T) {
	tests := []struct {
		name          string
		input         string
		expectedText  string
		expectedIndex int
		expectedScore float64
		expectError   bool
	}{
		{
			name:          "document as string with relevance_score",
			input:         `{"index": 0, "document": "This is a document", "relevance_score": 0.95}`,
			expectedText:  "This is a document",
			expectedIndex: 0,
			expectedScore: 0.95,
			expectError:   false,
		},
		{
			name:          "document as object with relevance_score",
			input:         `{"index": 1, "document": {"text": "This is a document"}, "relevance_score": 0.87}`,
			expectedText:  "This is a document",
			expectedIndex: 1,
			expectedScore: 0.87,
			expectError:   false,
		},
		{
			name:          "document as string with score field",
			input:         `{"index": 2, "document": "This is a document", "score": 0.92}`,
			expectedText:  "This is a document",
			expectedIndex: 2,
			expectedScore: 0.92,
			expectError:   false,
		},
		{
			name:          "document as object with score field",
			input:         `{"index": 3, "document": {"text": "This is a document"}, "score": 0.78}`,
			expectedText:  "This is a document",
			expectedIndex: 3,
			expectedScore: 0.78,
			expectError:   false,
		},
		{
			name:          "document as string with both score fields - relevance_score takes priority",
			input:         `{"index": 4, "document": "This is a document", "relevance_score": 0.95, "score": 0.80}`,
			expectedText:  "This is a document",
			expectedIndex: 4,
			expectedScore: 0.95,
			expectError:   false,
		},
		{
			name:          "document as object with both score fields - relevance_score takes priority",
			input:         `{"index": 5, "document": {"text": "This is a document"}, "relevance_score": 0.88, "score": 0.75}`,
			expectedText:  "This is a document",
			expectedIndex: 5,
			expectedScore: 0.88,
			expectError:   false,
		},
		{
			name:          "document as string with no score fields",
			input:         `{"index": 6, "document": "This is a document"}`,
			expectedText:  "This is a document",
			expectedIndex: 6,
			expectedScore: 0.0,
			expectError:   false,
		},
		{
			name:          "document as object with no score fields",
			input:         `{"index": 7, "document": {"text": "This is a document"}}`,
			expectedText:  "This is a document",
			expectedIndex: 7,
			expectedScore: 0.0,
			expectError:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var result RankResult
			err := json.Unmarshal([]byte(tt.input), &result)

			if tt.expectError {
				if err == nil {
					t.Errorf("Expected error but got none")
				}
				return
			}

			if err != nil {
				t.Fatalf("Unmarshal failed: %v", err)
			}

			if result.Document.Text != tt.expectedText {
				t.Errorf("Expected document text %q, got %q", tt.expectedText, result.Document.Text)
			}
			if result.Index != tt.expectedIndex {
				t.Errorf("Expected index %d, got %d", tt.expectedIndex, result.Index)
			}
			if result.RelevanceScore != tt.expectedScore {
				t.Errorf("Expected score %f, got %f", tt.expectedScore, result.RelevanceScore)
			}
		})
	}
}

// TestDocumentInfoMarshalJSON tests that DocumentInfo can be marshaled back to JSON
func TestDocumentInfoMarshalJSON(t *testing.T) {
	doc := DocumentInfo{Text: "Test document content"}

	data, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	expected := `{"text":"Test document content"}`
	if string(data) != expected {
		t.Errorf("Expected %s, got %s", expected, string(data))
	}
}

// TestRankResultMarshalJSON tests that RankResult can be marshaled back to JSON
func TestRankResultMarshalJSON(t *testing.T) {
	result := RankResult{
		Index:          1,
		Document:       DocumentInfo{Text: "Test document"},
		RelevanceScore: 0.95,
	}

	data, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	// Parse back to verify structure
	var parsed RankResult
	err = json.Unmarshal(data, &parsed)
	if err != nil {
		t.Fatalf("Round-trip unmarshal failed: %v", err)
	}

	if parsed.Index != result.Index {
		t.Errorf("Index mismatch: expected %d, got %d", result.Index, parsed.Index)
	}
	if parsed.Document.Text != result.Document.Text {
		t.Errorf("Document text mismatch: expected %q, got %q", result.Document.Text, parsed.Document.Text)
	}
	if parsed.RelevanceScore != result.RelevanceScore {
		t.Errorf("Score mismatch: expected %f, got %f", result.RelevanceScore, parsed.RelevanceScore)
	}
}
