package config

import "testing"

func TestDocumentTenantInflightLimit(t *testing.T) {
	if got := DocumentTenantInflightLimit(nil); got != 2 {
		t.Fatalf("default: %d", got)
	}
	for _, value := range []int{-1, 0, 1, 7} {
		cfg := &Config{KnowledgeBase: &KnowledgeBaseConfig{DocumentTenantInflight: value}}
		want := value
		if want <= 0 {
			want = 2
		}
		if got := DocumentTenantInflightLimit(cfg); got != want {
			t.Fatalf("value %d: got %d want %d", value, got, want)
		}
	}
}
