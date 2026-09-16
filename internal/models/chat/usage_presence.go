package chat

import (
	"bytes"
	"context"
	"io"
	"net/http"
)

type (
	usageEvidenceKey struct{}
	usageEvidence    struct{ body []byte }
)

// usageCapturingHTTPClient retains presence metadata discarded by the SDK's
// value-typed Usage field. It is enabled only for one unary call context.
type usageCapturingHTTPClient struct{ inner *http.Client }

func (c usageCapturingHTTPClient) Do(req *http.Request) (*http.Response, error) {
	resp, err := c.inner.Do(req)
	if err != nil {
		return resp, err
	}
	if evidence, ok := req.Context().Value(usageEvidenceKey{}).(*usageEvidence); ok {
		resp.Body = &usageEvidenceBody{ReadCloser: resp.Body, evidence: evidence}
	}
	return resp, nil
}

type usageEvidenceBody struct {
	io.ReadCloser
	evidence  *usageEvidence
	buffer    bytes.Buffer
	truncated bool
}

func (b *usageEvidenceBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if b.buffer.Len()+n <= 64<<20 && !b.truncated {
		b.buffer.Write(p[:n])
	} else {
		b.truncated = true
		b.buffer.Reset()
	}
	if err == io.EOF && !b.truncated {
		b.evidence.body = append([]byte(nil), b.buffer.Bytes()...)
	}
	return n, err
}

func (b *usageEvidenceBody) Close() error {
	if !b.truncated {
		b.evidence.body = append([]byte(nil), b.buffer.Bytes()...)
	}
	return b.ReadCloser.Close()
}

func captureUsage(ctx context.Context) (context.Context, *usageEvidence) {
	evidence := &usageEvidence{}
	return context.WithValue(ctx, usageEvidenceKey{}, evidence), evidence
}
