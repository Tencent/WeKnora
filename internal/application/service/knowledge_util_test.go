package service

import "testing"

func TestIsValidFileTypeAcceptsSupportedImageFormats(t *testing.T) {
	for _, name := range []string{
		"diagram.png",
		"diagram.webp",
		"diagram.bmp",
		"diagram.svg",
		"diagram.tiff",
	} {
		if !isValidFileType(name) {
			t.Fatalf("expected %q to be a valid file type", name)
		}
	}
}
