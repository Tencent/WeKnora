package api

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
)

// The remote VLM path sends its pictures as image_url parts in MultiContent,
// not in Images, so a strip that only cleared Images re-sent the identical
// request and the "retry without images" fallback could never succeed.
func TestStripImagesFromMessages_RemovesMultiContentImageParts(t *testing.T) {
	messages := []Message{{
		Role:   "user",
		Images: []string{"data:image/png;base64,AAA"},
		MultiContent: []MessageContentPart{
			{Type: "text", Text: "describe this"},
			{Type: "image_url", ImageURL: &ImageURL{URL: "data:image/png;base64,AAA"}},
		},
	}}

	cleaned := StripImagesFromMessages(messages)

	if cleaned[0].Images != nil {
		t.Errorf("Images = %v, want nil", cleaned[0].Images)
	}
	if len(cleaned[0].MultiContent) != 1 || cleaned[0].MultiContent[0].Type != "text" {
		t.Fatalf("MultiContent = %#v, want the text part only", cleaned[0].MultiContent)
	}
	// The caller's slice must survive: the retry is built from the same
	// messages the first attempt used.
	if len(messages[0].MultiContent) != 2 || len(messages[0].Images) != 1 {
		t.Fatalf("input mutated: %#v", messages[0])
	}
}

// An images-only turn loses MultiContent entirely rather than going out as an
// empty parts array, which some vendors reject.
func TestStripImagesFromMessages_DropsEmptiedMultiContent(t *testing.T) {
	cleaned := StripImagesFromMessages([]Message{{
		Role:    "user",
		Content: "what is this",
		MultiContent: []MessageContentPart{
			{Type: "image_url", ImageURL: &ImageURL{URL: "https://example.com/a.png"}},
		},
	}})
	if cleaned[0].MultiContent != nil {
		t.Fatalf("MultiContent = %#v, want nil", cleaned[0].MultiContent)
	}
	if cleaned[0].Content != "what is this" {
		t.Fatalf("Content = %q, want it untouched", cleaned[0].Content)
	}
}

// A stored image is referenced by the scheme of the backend holding it: an
// object-storage FileService returns "minio://bucket/key", "s3://bucket/key"
// and so on, optionally wrapped as "storage://<backendID>/<provider>://...".
// The predicate has to recognize the storage layer's scheme table, otherwise
// the handle travels to the vendor verbatim: remote APIs got "minio://..." as
// the image URL and the Ollama path dropped the picture without a word.
func TestResolveImageURLForStoredImages(t *testing.T) {
	pngBytes := []byte("\x89PNG\r\n\x1a\nstored image payload")
	resolved := map[string]bool{}
	originalResolver := LocalImageResolver
	LocalImageResolver = func(storageURL string) ([]byte, bool) {
		resolved[storageURL] = true
		return pngBytes, true
	}
	t.Cleanup(func() { LocalImageResolver = originalResolver })

	stored := []struct {
		name string
		url  string
	}{
		{name: "local", url: "local://10000/1/img.png"},
		{name: "minio", url: "minio://bucket/1/img.png"},
		{name: "s3", url: "s3://bucket/1/img.png"},
		{name: "cos", url: "cos://bucket/region/1/img.png"},
		{name: "tos", url: "tos://bucket/1/img.png"},
		{name: "oss", url: "oss://bucket/1/img.png"},
		{name: "ks3", url: "ks3://bucket/1/img.png"},
		{name: "resource reference", url: "resource://a1b2c3/1/img.png"},
		{name: "backend-wrapped provider", url: "storage://backend-a/cos://bucket/region/1/img.png"},
	}
	for _, tc := range stored {
		t.Run(tc.name, func(t *testing.T) {
			got := ResolveImageURLForLLM(tc.url)
			if !strings.HasPrefix(got, "data:image/png;base64,") {
				t.Fatalf("ResolveImageURLForLLM(%q) = %q, want a base64 PNG data URI", tc.url, got)
			}
			if data := ResolveImageURLForOllama(tc.url); !bytes.Equal(data, pngBytes) {
				t.Fatalf("ResolveImageURLForOllama(%q) = %q, want the stored bytes", tc.url, data)
			}
			if !resolved[tc.url] {
				t.Errorf("resolver was never asked for %q", tc.url)
			}
		})
	}
}

// A data: or http(s) URL is not application-stored: it must pass through
// untouched and must never be routed through the storage resolver.
func TestResolveImageURLForNonStoredURLs(t *testing.T) {
	originalResolver := LocalImageResolver
	LocalImageResolver = func(storageURL string) ([]byte, bool) {
		t.Errorf("resolver called for non-stored URL %q", storageURL)
		return nil, false
	}
	t.Cleanup(func() { LocalImageResolver = originalResolver })

	const dataURI = "data:image/png;base64,cGF5bG9hZA=="
	const remoteURL = "https://example.com/1/img.png"
	for _, tc := range []struct {
		name       string
		url        string
		wantOllama []byte
	}{
		{name: "data URI", url: dataURI, wantOllama: []byte("payload")},
		{name: "http URL", url: remoteURL},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := ResolveImageURLForLLM(tc.url); got != tc.url {
				t.Errorf("ResolveImageURLForLLM(%q) = %q, want it unchanged", tc.url, got)
			}
			if data := ResolveImageURLForOllama(tc.url); !bytes.Equal(data, tc.wantOllama) {
				t.Errorf("ResolveImageURLForOllama(%q) = %q, want %q", tc.url, data, tc.wantOllama)
			}
		})
	}
}

func TestIsMultimodalNotSupportedError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "explicit wording, no status",
			err:  fmt.Errorf("this model does not support image input"),
			want: true,
		},
		{
			name: "400 rejecting the image",
			err:  &HTTPError{StatusCode: 400, Body: `{"error":"invalid content type image_url"}`},
			want: true,
		},
		{
			name: "wrapped 400 still matches",
			err: fmt.Errorf("create chat completion: %w",
				&HTTPError{StatusCode: 400, Body: "image input rejected"}),
			want: true,
		},
		{
			// The old substring test fired here: HTTPError.Error() prints the
			// status, so every 5xx mentioning an image looked like "model is
			// not multimodal" and was retried stripped.
			name: "server error mentioning an image is not a capability verdict",
			err:  &HTTPError{StatusCode: 500, Body: "image upload backend unavailable"},
			want: false,
		},
		{
			// "400" also hides inside ordinary numbers in vendor bodies.
			name: "429 whose body merely contains 400",
			err:  &HTTPError{StatusCode: 429, Body: "image exceeds 4000 px, slow down"},
			want: false,
		},
		{
			name: "unrelated failure",
			err:  fmt.Errorf("context deadline exceeded"),
			want: false,
		},
		{name: "nil", err: nil, want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsMultimodalNotSupportedError(tc.err); got != tc.want {
				t.Errorf("IsMultimodalNotSupportedError(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}
