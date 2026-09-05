package docparser

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"io"
	"time"

	"github.com/Tencent/WeKnora/docreader/proto"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// LegacyDocPreviewMIME is the MIME type required for generated DOCX previews.
const LegacyDocPreviewMIME = "application/vnd.openxmlformats-officedocument.wordprocessingml.document"

// ErrLegacyDocPreviewUnavailable hides converter and validation details from callers.
var ErrLegacyDocPreviewUnavailable = errors.New("legacy Word preview unavailable")

// NormalizeLegacyDoc uses the existing authenticated connection and message caps.
// Conversion has no dependency on the parser engine chosen for ingestion.
func (p *GRPCDocumentReader) NormalizeLegacyDoc(ctx context.Context, source io.Reader) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	p.mu.RLock()
	client := p.client
	p.mu.RUnlock()
	if client == nil {
		return nil, ErrLegacyDocPreviewUnavailable
	}
	limit := getMaxMessageSize() - 4096 // protobuf envelope headroom
	if limit <= 0 {
		return nil, ErrLegacyDocPreviewUnavailable
	}
	content, err := io.ReadAll(io.LimitReader(source, int64(limit)+1))
	oleHeader := []byte{0xd0, 0xcf, 0x11, 0xe0, 0xa1, 0xb1, 0x1a, 0xe1}
	if err != nil || len(content) > limit || !bytes.HasPrefix(content, oleHeader) {
		return nil, ErrLegacyDocPreviewUnavailable
	}
	resp, err := client.NormalizeLegacyDoc(ctx, &proto.NormalizeLegacyDocRequest{
		FileContent: content,
		FileName:    "preview.doc",
	})
	if status.Code(err) == codes.ResourceExhausted && status.Convert(err).Message() == "Legacy Word preview busy" {
		return nil, interfaces.ErrLegacyDocPreviewBusy
	}
	invalidResponse := resp == nil || resp.ContentType != LegacyDocPreviewMIME ||
		resp.FileName != "preview.docx" || len(resp.FileContent) > limit
	if err != nil || invalidResponse {
		return nil, ErrLegacyDocPreviewUnavailable
	}
	if !validLegacyDocPreview(resp.FileContent) {
		return nil, ErrLegacyDocPreviewUnavailable
	}
	return resp.FileContent, nil
}

// ErrInvalidLegacyDocPreview indicates corrupt or oversized cached preview bytes.
var ErrInvalidLegacyDocPreview = errors.New("invalid cached Word preview")

// ReadLegacyDocPreview bounds and validates a cached representation before HTTP
// 200 is committed. In particular MinIO may only report NoSuchKey on Read.
func ReadLegacyDocPreview(source io.Reader) ([]byte, error) {
	limit := getMaxMessageSize() - 4096
	if limit <= 0 {
		return nil, ErrInvalidLegacyDocPreview
	}
	content, err := io.ReadAll(io.LimitReader(source, int64(limit)+1))
	if err != nil {
		return nil, err
	}
	if len(content) > limit || !validLegacyDocPreview(content) {
		return nil, ErrInvalidLegacyDocPreview
	}
	return content, nil
}

func validLegacyDocPreview(content []byte) bool {
	archive, err := zip.NewReader(bytes.NewReader(content), int64(len(content)))
	if err != nil {
		return false
	}
	var contentTypes, document bool
	for _, file := range archive.File {
		contentTypes = contentTypes || file.Name == "[Content_Types].xml"
		document = document || file.Name == "word/document.xml"
	}
	return contentTypes && document
}
