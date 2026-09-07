package service

import "testing"

func TestChunkHasExtractableText(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    bool
	}{
		{"empty", "", false},
		{"whitespace only", "  \n\t ", false},
		{"single image link", "![page_1.jpg](resource://abc)", false},
		{"several image links with newlines", "![p1](resource://a)\n\n![p2](resource://b)\n", false},
		{"prose", "第1章 総則", true},
		{"prose around an image link", "See figure: ![fig](resource://x) below.", true},
		{"image link followed by OCR text", "![page_1.jpg](resource://abc)\nSection 2: Definitions", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := chunkHasExtractableText(tt.content); got != tt.want {
				t.Fatalf("chunkHasExtractableText(%q) = %v, want %v", tt.content, got, tt.want)
			}
		})
	}
}
