package docparser

import (
	"bytes"
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/docreader/proto"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type legacyPreviewClient struct {
	proto.DocReaderClient
	response *proto.NormalizeLegacyDocResponse
	err      error
	calls    int
	t        *testing.T
	original []byte
}

func (c *legacyPreviewClient) NormalizeLegacyDoc(
	ctx context.Context,
	req *proto.NormalizeLegacyDocRequest,
	_ ...grpc.CallOption,
) (*proto.NormalizeLegacyDocResponse, error) {
	c.calls++
	if !bytes.Equal(req.FileContent, c.original) || req.FileName != "preview.doc" {
		c.t.Fatal("original request changed")
	}
	deadline, ok := ctx.Deadline()
	if !ok || time.Until(deadline) > 25*time.Second {
		c.t.Fatal("missing bounded deadline")
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	return c.response, c.err
}

func TestLegacyDocPreviewContract(t *testing.T) {
	original, err := os.ReadFile("../../../docreader/tests/fixtures/legacy_preview.doc")
	if err != nil {
		t.Fatal(err)
	}
	docx, err := os.ReadFile("../../../docreader/tests/fixtures/legacy_preview.docx")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name     string
		input    []byte
		response *proto.NormalizeLegacyDocResponse
		err      error
		want     bool
	}{
		{
			"success", original,
			&proto.NormalizeLegacyDocResponse{
				FileContent: docx, FileName: "preview.docx", ContentType: LegacyDocPreviewMIME,
			}, nil, true,
		},
		{
			"raw OLE response", original,
			&proto.NormalizeLegacyDocResponse{
				FileContent: original, FileName: "preview.docx", ContentType: LegacyDocPreviewMIME,
			}, nil, false,
		},
		{
			"wrong MIME", original,
			&proto.NormalizeLegacyDocResponse{
				FileContent: docx, FileName: "preview.docx", ContentType: "application/msword",
			}, nil, false,
		},
		{
			"wrong filename", original,
			&proto.NormalizeLegacyDocResponse{
				FileContent: docx, FileName: "preview.doc", ContentType: LegacyDocPreviewMIME,
			}, nil, false,
		},
		{"old server", original, nil, status.Error(codes.Unimplemented, "private detail"), false},
		{"timeout", original, nil, status.Error(codes.DeadlineExceeded, "private detail"), false},
		{"DOCX input", docx, nil, nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := &legacyPreviewClient{t: t, original: original, response: tc.response, err: tc.err}
			reader := &GRPCDocumentReader{client: client}
			result, err := reader.NormalizeLegacyDoc(context.Background(), bytes.NewReader(tc.input))
			if tc.want {
				if err != nil || !bytes.Equal(result, docx) {
					t.Fatalf("unexpected response %v", err)
				}
			} else if !errors.Is(err, ErrLegacyDocPreviewUnavailable) {
				t.Fatalf("unsanitized failure: %v", err)
			}
			if tc.name == "DOCX input" && client.calls != 0 {
				t.Fatal("sent DOCX to legacy converter")
			}
		})
	}
	t.Setenv("MAX_FILE_SIZE_MB", "1")
	client := &legacyPreviewClient{t: t, original: original}
	reader := &GRPCDocumentReader{client: client}
	large := append(append([]byte{}, original...), make([]byte, 1024*1024)...)
	_, err = reader.NormalizeLegacyDoc(context.Background(), bytes.NewReader(large))
	if err == nil || client.calls != 0 {
		t.Fatal("input limit bypassed")
	}
}

func TestLegacyDocPreviewBusyIsRetryable(t *testing.T) {
	original, err := os.ReadFile("../../../docreader/tests/fixtures/legacy_preview.doc")
	if err != nil {
		t.Fatal(err)
	}
	for _, message := range []string{"Legacy Word preview busy", "grpc received message larger than max"} {
		client := &legacyPreviewClient{t: t, original: original, err: status.Error(codes.ResourceExhausted, message)}
		reader := &GRPCDocumentReader{client: client}
		_, err := reader.NormalizeLegacyDoc(context.Background(), bytes.NewReader(original))
		if errors.Is(err, interfaces.ErrLegacyDocPreviewBusy) != (message == "Legacy Word preview busy") {
			t.Fatalf("incorrect busy classification: %v", err)
		}
	}
}

func TestReadLegacyDocPreviewRejectsCorruptionAndOversize(t *testing.T) {
	_, err := ReadLegacyDocPreview(bytes.NewBufferString("not a DOCX"))
	if !errors.Is(err, ErrInvalidLegacyDocPreview) {
		t.Fatal(err)
	}
	t.Setenv("MAX_FILE_SIZE_MB", "1")
	_, err = ReadLegacyDocPreview(bytes.NewReader(make([]byte, 1024*1024)))
	if !errors.Is(err, ErrInvalidLegacyDocPreview) {
		t.Fatal(err)
	}
}
