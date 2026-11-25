package docparser

import "testing"

// .aac is advertised by the attachment picker and accepted by temporary
// documents, but it was missing from the audio set. A file that reached the
// attachment processor as audio therefore fell through to the document reader,
// which has no ACC parser, and the user got "Failed to read document" instead
// of a transcript. Keep the audio set in sync with the formats we route to ASR.
func TestIsAudioFormatRecognizesAAC(t *testing.T) {
	for _, fileType := range []string{"aac", ".aac", "AAC", ".AAC"} {
		if !IsAudioFormat(fileType) {
			t.Errorf("IsAudioFormat(%q) = false, want true", fileType)
		}
	}
}

func TestIsAudioFormatStaysScopedToAudio(t *testing.T) {
	for _, fileType := range []string{"mp4", "mov", "pdf", "docx", "png", ""} {
		if IsAudioFormat(fileType) {
			t.Errorf("IsAudioFormat(%q) = true, want false", fileType)
		}
	}
}
