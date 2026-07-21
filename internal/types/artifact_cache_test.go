package types

import (
	"encoding/json"
	"fmt"
	"testing"
)

func TestArtifactCache_BeforeCreate(t *testing.T) {
	a := &ArtifactCache{}
	err := a.BeforeCreate(nil)
	if err != nil {
		t.Fatalf("BeforeCreate error: %v", err)
	}
	if a.ID == "" {
		t.Fatal("BeforeCreate did not set ID")
	}
}

func TestArtifactCache_TableName(t *testing.T) {
	a := ArtifactCache{}
	if a.TableName() != "artifact_caches" {
		t.Fatalf("unexpected table name: %s", a.TableName())
	}
}

func TestArtifactCacheJSONRoundtrip(t *testing.T) {
	vec := []float32{0.1, 0.2, 0.3}
	data, err := json.Marshal(vec)
	if err != nil {
		t.Fatal(err)
	}
	var vec2 []float32
	if err := json.Unmarshal(data, &vec2); err != nil {
		t.Fatal(err)
	}
	if len(vec2) != 3 || vec2[0] != 0.1 {
		t.Fatalf("vector JSON roundtrip failed: %v", vec2)
	}
}

func TestArtifactCacheOutputSize(t *testing.T) {
	a := &ArtifactCache{
		OutputText: "hello world",
		OutputSize: int64(len("hello world")),
	}
	if a.OutputSize != 11 {
		t.Fatalf("OutputSize: %d", a.OutputSize)
	}
	fmt.Println(a)
}
