package vlm

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// newChannelCheckingVLMServer stands in for Qwen-VL under vLLM, whose image
// processor takes three channels and answers a one-channel image with the
// HTTP 500 reported in #5528.
func newChannelCheckingVLMServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Messages []struct {
				Content []struct {
					ImageURL *struct {
						URL string `json:"url"`
					} `json:"image_url"`
				} `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		for _, m := range req.Messages {
			for _, part := range m.Content {
				if part.ImageURL == nil {
					continue
				}
				_, b64, _ := strings.Cut(part.ImageURL.URL, ",")
				data, err := base64.StdEncoding.DecodeString(b64)
				if err != nil {
					http.Error(w, err.Error(), http.StatusBadRequest)
					return
				}
				img, _, err := image.Decode(bytes.NewReader(data))
				if err != nil {
					http.Error(w, err.Error(), http.StatusBadRequest)
					return
				}
				switch img.(type) {
				case *image.Gray, *image.Gray16, *image.Paletted:
					b := img.Bounds()
					w.WriteHeader(http.StatusInternalServerError)
					_, _ = fmt.Fprintf(w, `{"object":"error","message":"Internal server error: `+
						`output with shape [1, 1, %d, %d] doesn't match the broadcast shape [1, 3, %d, %d]",`+
						`"type":"InternalServerError","code":500}`, b.Dy(), b.Dx(), b.Dy(), b.Dx())
					return
				}
			}
		}
		_, _ = w.Write([]byte(`{"id":"chatcmpl-test","object":"chat.completion","choices":[
			{"index":0,"message":{"role":"assistant","content":"a scanned page"},"finish_reason":"stop"}]}`))
	}))
}

// TestRemoteAPIVLMSendsGrayscaleImagesAsRGB is the regression test for #5528:
// grayscale scans reached the VLM as stored, and a three-channel processor
// failed every retry on them.
func TestRemoteAPIVLMSendsGrayscaleImagesAsRGB(t *testing.T) {
	withVLMSSRFWhitelist(t, "127.0.0.1")
	server := newChannelCheckingVLMServer(t)
	defer server.Close()

	scan := image.NewGray(image.Rect(0, 0, 928, 992))
	for i := range scan.Pix {
		scan.Pix[i] = uint8(i)
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, scan); err != nil {
		t.Fatal(err)
	}

	v, err := NewRemoteAPIVLM(&Config{BaseURL: server.URL, ModelName: "qwen3-vl", APIKey: "sk-test"})
	if err != nil {
		t.Fatalf("NewRemoteAPIVLM: %v", err)
	}
	got, err := v.Predict(t.Context(), [][]byte{buf.Bytes()}, "describe the image")
	if err != nil {
		t.Fatalf("Predict(grayscale PNG): %v", err)
	}
	if got != "a scanned page" {
		t.Errorf("Predict = %q, want the caption", got)
	}
}
