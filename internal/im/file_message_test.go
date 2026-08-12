package im

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/Tencent/WeKnora/internal/types"
)

type attachmentTestAdapter struct {
	*lifecycleTestAdapter
	content  []byte
	fileName string
}

func (a *attachmentTestAdapter) DownloadFile(context.Context, *IncomingMessage) (io.ReadCloser, string, error) {
	return io.NopCloser(bytes.NewReader(a.content)), a.fileName, nil
}

func TestFileMessageQAContent(t *testing.T) {
	tests := []struct {
		name string
		msg  *IncomingMessage
		want string
	}{
		{
			name: "preserves caption",
			msg:  &IncomingMessage{Content: "请总结这个文件", FileName: "report.pdf"},
			want: "请总结这个文件",
		},
		{
			name: "builds query for file-only event",
			msg:  &IncomingMessage{FileName: "report.pdf"},
			want: "我上传了文件「report.pdf」。请确认已收到，并告知我接下来可以如何协助。",
		},
		{
			name: "uses safe name when platform omits filename",
			msg:  &IncomingMessage{},
			want: "我上传了文件「未命名文件」。请确认已收到，并告知我接下来可以如何协助。",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := fileMessageQAContent(tt.msg); got != tt.want {
				t.Errorf("fileMessageQAContent() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestApplyIMAttachmentTruncationByLineCount(t *testing.T) {
	lines := make([]string, maxIMAttachmentLines+1)
	for i := range lines {
		lines[i] = "line"
	}
	attachment := &types.MessageAttachment{}
	applyIMAttachmentTruncation(strings.Join(lines, "\n"), attachment)

	if !attachment.IsTruncated {
		t.Fatal("expected content to be truncated")
	}
	if attachment.LineCount != maxIMAttachmentLines+1 {
		t.Errorf("LineCount = %d, want %d", attachment.LineCount, maxIMAttachmentLines+1)
	}
	if got := len(strings.Split(attachment.Content, "\n")); got != maxIMAttachmentLines {
		t.Errorf("kept lines = %d, want %d", got, maxIMAttachmentLines)
	}
}

func TestApplyIMAttachmentTruncationLimitsLargeSingleLine(t *testing.T) {
	attachment := &types.MessageAttachment{}
	applyIMAttachmentTruncation(strings.Repeat("x", 20<<20), attachment)

	if !attachment.IsTruncated {
		t.Fatal("expected content to be truncated")
	}
	if got := len(attachment.Content); got != maxIMAttachmentContentBytes {
		t.Fatalf("content bytes = %d, want %d", got, maxIMAttachmentContentBytes)
	}
	if attachment.LineCount != 1 {
		t.Fatalf("LineCount = %d, want 1", attachment.LineCount)
	}
}

func TestApplyIMAttachmentTruncationPreservesUTF8(t *testing.T) {
	attachment := &types.MessageAttachment{}
	applyIMAttachmentTruncation(strings.Repeat("中", maxIMAttachmentContentBytes), attachment)

	if !attachment.IsTruncated {
		t.Fatal("expected content to be truncated")
	}
	if !utf8.ValidString(attachment.Content) {
		t.Fatal("truncated content is not valid UTF-8")
	}
	if len(attachment.Content) > maxIMAttachmentContentBytes {
		t.Fatalf("content bytes = %d, exceeds %d", len(attachment.Content), maxIMAttachmentContentBytes)
	}
}

func TestPrepareIMAttachmentsDetectsImageMIMEFromContent(t *testing.T) {
	// The platform may name a JPEG resource with a .png suffix. The data URI
	// must use its actual content type for vision model compatibility.
	jpeg := []byte{0xff, 0xd8, 0xff, 0xe0, 0x00, 0x10, 'J', 'F', 'I', 'F', 0x00, 0x01}
	adapter := &attachmentTestAdapter{
		lifecycleTestAdapter: &lifecycleTestAdapter{},
		content:              jpeg,
		fileName:             "platform-image.png",
	}

	attachments, imageURLs, _, err := (&Service{}).prepareIMAttachments(context.Background(), &IncomingMessage{
		MessageType: MessageTypeImage,
		FileName:    "platform-image.png",
	}, adapter)
	if err != nil {
		t.Fatalf("prepareIMAttachments() error = %v", err)
	}
	if len(attachments) != 1 {
		t.Fatalf("attachment count = %d, want 1", len(attachments))
	}
	if len(imageURLs) != 1 || !strings.HasPrefix(imageURLs[0], "data:image/jpeg;base64,") {
		t.Fatalf("image URL = %v, want JPEG data URI", imageURLs)
	}
}
