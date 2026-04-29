package types

import (
	"encoding/json"
	"testing"
)

func TestWikiPageTypes(t *testing.T) {
	// Verify all page type constants are non-empty and unique
	pageTypes := []string{
		WikiPageTypeSummary,
		WikiPageTypeEntity,
		WikiPageTypeConcept,
		WikiPageTypeIndex,
		WikiPageTypeLog,
		WikiPageTypeSynthesis,
		WikiPageTypeComparison,
	}
	seen := make(map[string]bool)
	for _, pt := range pageTypes {
		if pt == "" {
			t.Errorf("WikiPageType constant is empty")
		}
		if seen[pt] {
			t.Errorf("Duplicate WikiPageType: %s", pt)
		}
		seen[pt] = true
	}
}

func TestWikiConfigValueScan(t *testing.T) {
	config := WikiConfig{
		SynthesisModelID:  "model-123",
		MaxPagesPerIngest: 20,
	}

	val, err := config.Value()
	if err != nil {
		t.Fatalf("WikiConfig.Value() error: %v", err)
	}

	var restored WikiConfig
	b, ok := val.([]byte)
	if !ok {
		t.Fatal("WikiConfig.Value() did not return []byte")
	}
	if err := restored.Scan(b); err != nil {
		t.Fatalf("WikiConfig.Scan() error: %v", err)
	}

	if restored.SynthesisModelID != "model-123" {
		t.Error("SynthesisModelID mismatch")
	}
	if restored.MaxPagesPerIngest != 20 {
		t.Error("MaxPagesPerIngest mismatch")
	}
}

func TestWikiConfigScanNil(t *testing.T) {
	var config WikiConfig
	if err := config.Scan(nil); err != nil {
		t.Fatalf("Scan(nil) should not error: %v", err)
	}
}

func TestStringArrayValueScan(t *testing.T) {
	arr := StringArray{"a", "b", "c"}

	val, err := arr.Value()
	if err != nil {
		t.Fatalf("StringArray.Value() error: %v", err)
	}

	var restored StringArray
	b, ok := val.([]byte)
	if !ok {
		t.Fatal("StringArray.Value() did not return []byte")
	}
	if err := restored.Scan(b); err != nil {
		t.Fatalf("StringArray.Scan() error: %v", err)
	}

	if len(restored) != 3 || restored[0] != "a" || restored[1] != "b" || restored[2] != "c" {
		t.Errorf("StringArray round-trip failed: got %v", restored)
	}
}

func TestStringArrayEmpty(t *testing.T) {
	// nil StringArray marshals to JSON null via json.Marshal
	var arr StringArray
	val, err := arr.Value()
	if err != nil {
		t.Fatalf("nil StringArray.Value() error: %v", err)
	}
	// json.Marshal(nil) returns "null", which is valid
	if val == nil {
		// Some implementations return nil for nil slices
		return
	}
	b, ok := val.([]byte)
	if !ok {
		t.Fatalf("unexpected type: %T", val)
	}
	s := string(b)
	if s != "null" && s != "[]" {
		t.Errorf("nil StringArray.Value() should return 'null' or '[]', got %q", s)
	}
}

func TestKnowledgeBaseEnsureDefaultsWiki(t *testing.T) {
	kb := &KnowledgeBase{
		Type:       KnowledgeBaseTypeDocument,
		WikiConfig: &WikiConfig{SynthesisModelID: "m-1"},
	}
	kb.EnsureDefaults()

	if kb.WikiConfig == nil {
		t.Fatal("EnsureDefaults should preserve WikiConfig for wiki-enabled KB")
	}
	if kb.WikiConfig.SynthesisModelID != "m-1" {
		t.Error("SynthesisModelID should be preserved")
	}
	if kb.FAQConfig != nil {
		t.Error("Document KB should not have FAQConfig")
	}
}

func TestKnowledgeBaseEnsureDefaultsDocumentWithoutWiki(t *testing.T) {
	kb := &KnowledgeBase{
		Type: KnowledgeBaseTypeDocument,
	}
	kb.EnsureDefaults()

	// WikiConfig should remain nil when not set
	if kb.WikiConfig != nil {
		t.Error("Document KB without wiki should not have WikiConfig after EnsureDefaults")
	}
}

func TestWikiPageJSON(t *testing.T) {
	page := WikiPage{
		ID:         "test-id",
		Slug:       "entity/test",
		Title:      "Test Entity",
		PageType:   WikiPageTypeEntity,
		Content:    "# Test\n\nSome content with [[concept/related]]",
		Summary:    "A test entity page",
		SourceRefs: StringArray{"source-1", "source-2"},
		OutLinks:   StringArray{"concept/related"},
		InLinks:    StringArray{"summary/doc1"},
		Version:    3,
	}

	// Serialize
	data, err := json.Marshal(page)
	if err != nil {
		t.Fatalf("Marshal error: %v", err)
	}

	// Deserialize
	var restored WikiPage
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}

	if restored.Slug != "entity/test" {
		t.Errorf("Slug mismatch: got %s", restored.Slug)
	}
	if restored.Version != 3 {
		t.Errorf("Version mismatch: got %d", restored.Version)
	}
	if len(restored.OutLinks) != 1 || restored.OutLinks[0] != "concept/related" {
		t.Errorf("OutLinks mismatch: got %v", restored.OutLinks)
	}
	if len(restored.SourceRefs) != 2 {
		t.Errorf("SourceRefs mismatch: got %v", restored.SourceRefs)
	}
}

func TestWikiGraphDataJSON(t *testing.T) {
	graph := WikiGraphData{
		Nodes: []WikiGraphNode{
			{Slug: "entity/a", Title: "A", PageType: "entity", LinkCount: 2},
			{Slug: "concept/b", Title: "B", PageType: "concept", LinkCount: 1},
		},
		Edges: []WikiGraphEdge{
			{Source: "entity/a", Target: "concept/b"},
		},
	}

	data, err := json.Marshal(graph)
	if err != nil {
		t.Fatalf("Marshal error: %v", err)
	}

	var restored WikiGraphData
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}

	if len(restored.Nodes) != 2 {
		t.Errorf("Nodes count mismatch: got %d", len(restored.Nodes))
	}
	if len(restored.Edges) != 1 {
		t.Errorf("Edges count mismatch: got %d", len(restored.Edges))
	}
	if restored.Edges[0].Source != "entity/a" || restored.Edges[0].Target != "concept/b" {
		t.Errorf("Edge mismatch: got %v", restored.Edges[0])
	}
}

func TestChunkTypeWikiPage(t *testing.T) {
	if ChunkTypeWikiPage != "wiki_page" {
		t.Errorf("ChunkTypeWikiPage should be 'wiki_page', got '%s'", ChunkTypeWikiPage)
	}
}

func TestWikiExtractionGranularity_IsValid(t *testing.T) {
	valid := []WikiExtractionGranularity{
		WikiExtractionFocused,
		WikiExtractionStandard,
		WikiExtractionExhaustive,
	}
	for _, g := range valid {
		if !g.IsValid() {
			t.Errorf("%q should be valid", g)
		}
	}

	invalid := []WikiExtractionGranularity{"", "FOCUSED", "strict", "none", "FULL"}
	for _, g := range invalid {
		if g.IsValid() {
			t.Errorf("%q should NOT be valid", g)
		}
	}
}

func TestWikiExtractionGranularity_Normalize(t *testing.T) {
	cases := map[WikiExtractionGranularity]WikiExtractionGranularity{
		"":           WikiExtractionStandard, // legacy / unset
		"unknown":    WikiExtractionStandard,
		"FOCUSED":    WikiExtractionStandard, // case-sensitive on purpose
		"focused":    WikiExtractionFocused,
		"standard":   WikiExtractionStandard,
		"exhaustive": WikiExtractionExhaustive,
	}
	for in, want := range cases {
		if got := in.Normalize(); got != want {
			t.Errorf("Normalize(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestWikiConfig_JSONRoundTrip_WithGranularity(t *testing.T) {
	original := WikiConfig{
		SynthesisModelID:      "m-1",
		MaxPagesPerIngest:     20,
		ExtractionGranularity: WikiExtractionFocused,
	}
	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal error: %v", err)
	}
	var restored WikiConfig
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}
	if restored.ExtractionGranularity != WikiExtractionFocused {
		t.Errorf("granularity round-trip failed: got %q", restored.ExtractionGranularity)
	}

	// Legacy row without the granularity field. We also include the removed
	// "enabled" / "auto_ingest" keys to verify the decoder tolerates unknown
	// fields on rows written before those columns were retired.
	legacy := []byte(`{"enabled":true,"auto_ingest":true,"synthesis_model_id":"","max_pages_per_ingest":0}`)
	var oldCfg WikiConfig
	if err := json.Unmarshal(legacy, &oldCfg); err != nil {
		t.Fatalf("Unmarshal legacy error: %v", err)
	}
	if oldCfg.ExtractionGranularity != "" {
		t.Errorf("legacy row should decode to empty granularity, got %q", oldCfg.ExtractionGranularity)
	}
	if oldCfg.ExtractionGranularity.Normalize() != WikiExtractionStandard {
		t.Errorf("legacy row should normalize to standard")
	}
}
