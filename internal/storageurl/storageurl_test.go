package storageurl

import (
	"context"
	"errors"
	"io"
	"mime/multipart"
	"testing"

	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubFileService implements interfaces.FileService; only GetFileURL matters here.
type stubFileService struct {
	getFileURL func(ctx context.Context, filePath string) (string, error)
	calls      int
}

func (s *stubFileService) CheckConnectivity(context.Context) error { return nil }

func (s *stubFileService) SaveFile(context.Context, *multipart.FileHeader, uint64, string) (string, error) {
	return "", nil
}

func (s *stubFileService) SaveBytes(context.Context, []byte, uint64, string, bool) (string, error) {
	return "", nil
}

func (s *stubFileService) GetFile(context.Context, string) (io.ReadCloser, error) { return nil, nil }

func (s *stubFileService) GetFileURL(ctx context.Context, filePath string) (string, error) {
	s.calls++
	if s.getFileURL != nil {
		return s.getFileURL(ctx, filePath)
	}
	return "https://cdn.example.com/" + filePath, nil
}

func (s *stubFileService) DeleteFile(context.Context, string) error { return nil }

func (s *stubFileService) CopyFile(context.Context, string, uint64, string) (string, error) {
	return "", nil
}

// fixedResolver returns the same FileService for every reference.
type fixedResolver struct{ svc interfaces.FileService }

func (r fixedResolver) ResolveFileService(string) interfaces.FileService { return r.svc }

func stubResolver(url string) Resolver {
	return fixedResolver{svc: &stubFileService{
		getFileURL: func(context.Context, string) (string, error) { return url, nil },
	}}
}

func TestRewriter_RewritesEveryReferenceForm(t *testing.T) {
	svc := &stubFileService{
		getFileURL: func(context.Context, string) (string, error) {
			return "https://cdn.example.com/signed.png", nil
		},
	}
	w := NewRewriter(fixedResolver{svc: svc}, "TEST")

	in := "handle ![a](resource://xifDo7NTSL300Lp1goVutw) " +
		"legacy ![b](minio://bucket/10000/exports/b.png) " +
		"scoped ![c](storage://backend-a/cos://bucket/ap/10000/exports/c.png)"
	out := w.String(context.Background(), in)

	assert.NotContains(t, out, "resource://")
	assert.NotContains(t, out, "minio://")
	assert.NotContains(t, out, "storage://")
	assert.Equal(t, 3, svc.calls)
}

// An already-public URL in the answer must be left alone.
func TestRewriter_LeavesHTTPURLsAlone(t *testing.T) {
	w := NewRewriter(stubResolver("https://cdn.example.com/x.png"), "TEST")
	in := "![a](https://example.com/a.png) and ![b](http://example.com/b.png)"
	assert.Equal(t, in, w.String(context.Background(), in))
}

// Emitting an unfetchable URL is worse than leaving the handle: the client can
// still fall back to the authenticated /files proxy for a handle.
func TestRewriter_NonHTTPResultIsNoOp(t *testing.T) {
	w := NewRewriter(stubResolver("storage://7cb970a6/oss://bucket/10000/exports/a.png"), "TEST")
	in := "![img](resource://xifDo7NTSL300Lp1goVutw)"
	assert.Equal(t, in, w.String(context.Background(), in))
}

func TestRewriter_ResolveFailureIsNoOp(t *testing.T) {
	w := NewRewriter(fixedResolver{svc: &stubFileService{
		getFileURL: func(context.Context, string) (string, error) {
			return "", errors.New("backend unreachable")
		},
	}}, "TEST")
	in := "![img](resource://xifDo7NTSL300Lp1goVutw)"
	assert.Equal(t, in, w.String(context.Background(), in))
}

func TestRewriter_UnknownBackendIsNoOp(t *testing.T) {
	w := NewRewriter(fixedResolver{svc: nil}, "TEST")
	in := "![img](resource://xifDo7NTSL300Lp1goVutw)"
	assert.Equal(t, in, w.String(context.Background(), in))
}

// Uppercase schemes are valid per RFC 3986 §3.1 (e.g. an OBS_PROXY_DOMAIN
// configured as HTTPS://…) and must be substituted, not dropped.
func TestRewriter_UppercaseSchemeIsSubstituted(t *testing.T) {
	w := NewRewriter(stubResolver("HTTPS://cdn.example.com/x.png"), "TEST")
	out := w.String(context.Background(), "![img](resource://xifDo7NTSL300Lp1goVutw)")
	assert.Contains(t, out, "HTTPS://cdn.example.com/x.png")
	assert.NotContains(t, out, "resource://")
}

// Each resource:// resolution writes an access-grant row, so a repeated image
// must be resolved once per request.
func TestRewriter_MemoisesRepeatedReferences(t *testing.T) {
	svc := &stubFileService{}
	w := NewRewriter(fixedResolver{svc: svc}, "TEST")
	ctx := context.Background()

	ref := "resource://xifDo7NTSL300Lp1goVutw"
	first := w.String(ctx, "![a]("+ref+")")
	second := w.String(ctx, "![b]("+ref+")")

	assert.Equal(t, 1, svc.calls, "the same reference must resolve once per Rewriter")
	assert.Equal(t, "https://cdn.example.com/"+ref, first[5:len(first)-1])
	assert.Contains(t, second, "https://cdn.example.com/"+ref)
}

func TestRewriter_DisabledWithoutResolver(t *testing.T) {
	w := NewRewriter(nil, "TEST")
	in := "![img](resource://xifDo7NTSL300Lp1goVutw)"
	assert.False(t, w.Enabled())
	assert.Equal(t, in, w.String(context.Background(), in))
	assert.Equal(t, in, w.Ref(context.Background(), in))
}

// Ref handles a bare reference such as MessageImage.URL, and must not touch a
// value that is not a reference at all.
func TestRewriter_Ref(t *testing.T) {
	w := NewRewriter(stubResolver("https://cdn.example.com/x.png"), "TEST")
	ctx := context.Background()

	assert.Equal(t, "https://cdn.example.com/x.png",
		w.Ref(ctx, "resource://xifDo7NTSL300Lp1goVutw"))
	assert.Equal(t, "", w.Ref(ctx, ""))
	assert.Equal(t, "data:image/png;base64,AAAA", w.Ref(ctx, "data:image/png;base64,AAAA"))
}

func TestIsHTTPURL(t *testing.T) {
	for _, s := range []string{"http://a", "https://a", "HTTP://a", "HTTPS://a"} {
		assert.True(t, IsHTTPURL(s), s)
	}
	for _, s := range []string{"", "ftp://a", "resource://abc", "local://1/a.png", "http:/"} {
		assert.False(t, IsHTTPURL(s), s)
	}
}

func TestParseMode(t *testing.T) {
	tests := []struct {
		in      string
		want    Mode
		wantErr bool
	}{
		{"", ModeHandle, false},
		{"handle", ModeHandle, false},
		{"public", ModePublic, false},
		{"  PUBLIC ", ModePublic, false},
		{"true", ModeHandle, true},
		{"signed", ModeHandle, true},
	}
	for _, tt := range tests {
		got, err := ParseMode(tt.in)
		if tt.wantErr {
			require.Error(t, err, "ParseMode(%q)", tt.in)
		} else {
			require.NoError(t, err, "ParseMode(%q)", tt.in)
		}
		assert.Equal(t, tt.want, got, "ParseMode(%q)", tt.in)
	}
}
