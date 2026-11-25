package session

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/infrastructure/docparser"
	secutils "github.com/Tencent/WeKnora/internal/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The chat attachment gate used to be a fourth hand-written extension list. It
// refused .xmind — which AttachmentUpload.vue offers, temporary documents
// accept, and docreader routes to XMindParser — and it refused every other
// format the shared import set accepts (.odt, .rtf, .docm, .xlsm, .ods, .pptm,
// .odp, .htm) that the picker adds once a parser engine advertises it. These
// tests pin the gate to the shared set so the two cannot drift again.
func TestAttachmentGateAcceptsEverySharedImportExtension(t *testing.T) {
	extensions := secutils.SupportedImportExtensions()
	require.NotEmpty(t, extensions)

	for _, ext := range extensions {
		if !isValidFileType("report." + ext) {
			t.Errorf("shared import extension %q is refused for chat attachments", ext)
		}
	}
}

func TestAttachmentGateAcceptsFormatsHandledWithoutADocumentParser(t *testing.T) {
	// Line-based text reader.
	for _, name := range []string{"trace.log", "config.yaml", "config.yml", "feed.xml"} {
		assert.True(t, isValidFileType(name), "%s is read by the inline text reader", name)
	}
	// ASR transcription instead of parsing: .xmind is a document, .aac is audio.
	assert.True(t, isValidFileType("roadmap.xmind"), ".xmind is offered by the picker and parsed by XMindParser")
	assert.True(t, isValidFileType("memo.aac"), ".aac is audio and goes to ASR, not to a document parser")
}

func TestAttachmentGateNormalizesCaseAndLeadingDot(t *testing.T) {
	for _, name := range []string{"Photo.PNG", "scan.Pdf", "Memo.AAC", "Sheet.XLSX"} {
		assert.True(t, isValidFileType(name), "%s should be judged on its lower-cased extension", name)
	}
}

func TestAttachmentGateRejectsUnsupportedAndExtensionless(t *testing.T) {
	for _, name := range []string{"setup.exe", "archive.7z", "payload.sh", "README", ""} {
		assert.False(t, isValidFileType(name), "%s must not be accepted as an attachment", name)
	}
}

// A file without an extension is rejected, but the reason has to name what the
// caller sent. The gate used to default ext to ".txt" before validating, so the
// error told the user that ".txt is unsupported" — a format the gate accepts.
func TestProcessAttachmentReportsTheExtensionItActuallyRejected(t *testing.T) {
	_, err := (&AttachmentProcessor{}).ProcessAttachment(
		context.Background(), []byte("hello"), "README", 5, 1, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "(none)")
	assert.NotContains(t, err.Error(), ".txt")

	_, err = (&AttachmentProcessor{}).ProcessAttachment(
		context.Background(), []byte("hello"), "setup.exe", 5, 1, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), ".exe")
}

// The temporary-document upload gate decides whether a request needs an ASR
// model; the parse worker decides whether to transcribe. When those two
// disagree the upload is accepted, ASR is wired up, and then the worker picks
// the document path instead, which fails for a format docreader has no parser
// for. Keep the gate reading the worker's own set.
func TestTemporaryDocumentAudioGateMatchesTheParseWorker(t *testing.T) {
	for _, ext := range []string{"mp3", "wav", "m4a", "flac", "ogg", "aac", "webm", "mp4", "pdf", "docx"} {
		assert.Equal(t, docparser.IsAudioFormat(ext), isAudioExtension(ext),
			"upload gate and parse worker disagree about %q", ext)
	}
	assert.True(t, isAudioExtension("aac"), ".aac is offered as audio by the picker")
}
