package service

import "testing"

func TestContentAddressedChunkID(t *testing.T) {
	a := contentAddressedChunkID("doc", "text", 3, "same\n content")
	if got := contentAddressedChunkID("doc", "text", 3, " same   content "); got != a {
		t.Fatalf("normalized content should retain its id: %s != %s", got, a)
	}
	for name, got := range map[string]string{
		"document": contentAddressedChunkID("other", "text", 3, "same content"),
		"kind":     contentAddressedChunkID("doc", "ocr", 3, "same content"),
		"position": contentAddressedChunkID("doc", "text", 4, "same content"),
		"content":  contentAddressedChunkID("doc", "text", 3, "changed"),
	} {
		if got == a {
			t.Fatalf("changing %s must invalidate the address", name)
		}
	}
}
