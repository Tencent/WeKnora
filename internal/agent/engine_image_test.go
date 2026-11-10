package agent

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestDecodeDataURIBytes_Valid(t *testing.T) {
	raw := []byte{0xFF, 0xD8, 0xFF} // minimal bytes
	encoded := base64.StdEncoding.EncodeToString(raw)
	dataURI := "data:image/jpeg;base64," + encoded

	decoded, err := decodeDataURIBytes(dataURI)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(decoded) != len(raw) {
		t.Errorf("expected %d bytes, got %d", len(raw), len(decoded))
	}
}

func TestDecodeDataURIBytes_NoPaddingFallback(t *testing.T) {
	raw := []byte{0x01, 0x02, 0x03, 0x04, 0x05}
	// RawStdEncoding omits padding '='
	encoded := base64.RawStdEncoding.EncodeToString(raw)
	dataURI := "data:image/png;base64," + encoded

	decoded, err := decodeDataURIBytes(dataURI)
	if err != nil {
		t.Fatalf("unexpected error with padding fallback: %v", err)
	}
	if len(decoded) != len(raw) {
		t.Errorf("expected %d bytes, got %d", len(raw), len(decoded))
	}
}

func TestDecodeDataURIBytes_NoDataPrefix(t *testing.T) {
	_, err := decodeDataURIBytes("image/png;base64,AAAA")
	if err == nil {
		t.Fatal("expected error for missing data: prefix")
	}
	if !strings.Contains(err.Error(), "not a data URI") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestDecodeDataURIBytes_NoBase64Marker(t *testing.T) {
	_, err := decodeDataURIBytes("data:image/png,rawdata")
	if err == nil {
		t.Fatal("expected error for missing ;base64, marker")
	}
	if !strings.Contains(err.Error(), "unsupported") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestDecodeDataURIBytes_EmptyBase64(t *testing.T) {
	decoded, err := decodeDataURIBytes("data:image/png;base64,")
	if err != nil {
		t.Fatalf("unexpected error for empty base64: %v", err)
	}
	if len(decoded) != 0 {
		t.Errorf("expected 0 bytes for empty base64, got %d", len(decoded))
	}
}

func TestDescribeImages_WithDescriber(t *testing.T) {
	var prompt string
	engine := &AgentEngine{
		imageDescriber: func(_ context.Context, _ []byte, got string) (string, error) {
			prompt = got
			return "A red square image", nil
		},
	}

	raw := []byte{0x89, 0x50, 0x4E, 0x47} // fake PNG header
	dataURI := "data:image/png;base64," + base64.StdEncoding.EncodeToString(raw)

	descriptions := engine.describeImages(context.Background(), []string{dataURI}, nil)
	if len(descriptions) != 1 {
		t.Fatalf("expected 1 description, got %d", len(descriptions))
	}
	if descriptions[0] != "A red square image" {
		t.Errorf("unexpected description: %s", descriptions[0])
	}

	exported := engine.DescribeDataURIImages(context.Background(), []string{dataURI})
	if len(exported) != 1 || exported[0] != "A red square image" {
		t.Fatalf("exported description = %v", exported)
	}
	if prompt != toolImageFullPrompt {
		t.Fatalf("upload prompt = %q", prompt)
	}
}

func TestDescribeImages_VLMFailure(t *testing.T) {
	engine := &AgentEngine{
		imageDescriber: func(ctx context.Context, imgBytes []byte, prompt string) (string, error) {
			return "", errors.New("VLM service unavailable")
		},
	}

	raw := []byte{0x89, 0x50}
	dataURI := "data:image/png;base64," + base64.StdEncoding.EncodeToString(raw)

	descriptions := engine.describeImages(context.Background(), []string{dataURI}, nil)
	if len(descriptions) != 1 || descriptions[0] != "" {
		t.Errorf("expected one empty description on VLM failure, got %q", descriptions)
	}
}

func TestDescribeImages_InvalidDataURI(t *testing.T) {
	engine := &AgentEngine{
		imageDescriber: func(ctx context.Context, imgBytes []byte, prompt string) (string, error) {
			t.Fatal("imageDescriber should not be called for invalid data URI")
			return "", nil
		},
	}

	descriptions := engine.describeImages(context.Background(), []string{"not-a-data-uri"}, nil)
	if len(descriptions) != 1 || descriptions[0] != "" {
		t.Errorf("expected one empty description for invalid URI, got %q", descriptions)
	}
}

func TestDescribeImages_ContextCancelled(t *testing.T) {
	var callCount atomic.Int32
	engine := &AgentEngine{
		imageDescriber: func(ctx context.Context, imgBytes []byte, prompt string) (string, error) {
			callCount.Add(1)
			return "desc", nil
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	raw := []byte{0x89}
	dataURI := "data:image/png;base64," + base64.StdEncoding.EncodeToString(raw)

	descriptions := engine.describeImages(ctx, []string{dataURI, dataURI, dataURI}, nil)
	if n := callCount.Load(); n != 0 {
		t.Errorf("expected 0 VLM calls with cancelled context, got %d", n)
	}
	if strings.Join(descriptions, "") != "" {
		t.Errorf("expected no descriptions, got %q", descriptions)
	}
}

func TestDescribeImages_NilDescriber(t *testing.T) {
	engine := &AgentEngine{
		imageDescriber: nil,
	}

	// Should not panic even with nil describer
	descriptions := engine.describeImages(context.Background(), []string{"data:image/png;base64,AAAA"}, nil)
	if strings.Join(descriptions, "") != "" {
		t.Errorf("expected no descriptions with nil describer, got %q", descriptions)
	}
}

func TestDescribeImages_MixedSuccessStaysAligned(t *testing.T) {
	engine := &AgentEngine{
		imageDescriber: func(ctx context.Context, imgBytes []byte, prompt string) (string, error) {
			if string(imgBytes) == "2" {
				return "", errors.New("fail on second")
			}
			return "desc " + string(imgBytes), nil
		},
	}
	uris := []string{
		"data:image/png;base64," + base64.StdEncoding.EncodeToString([]byte("1")),
		"data:image/png;base64," + base64.StdEncoding.EncodeToString([]byte("2")),
		"data:image/png;base64," + base64.StdEncoding.EncodeToString([]byte("3")),
	}

	descriptions := engine.describeImages(context.Background(), uris, nil)
	want := []string{"desc 1", "", "desc 3"}
	if strings.Join(descriptions, "|") != strings.Join(want, "|") {
		t.Errorf("descriptions = %q; want %q", descriptions, want)
	}
}

func TestDescribeImages_RunsConcurrentlyWithPerImageTimeout(t *testing.T) {
	var running, peak atomic.Int32
	release := make(chan struct{})
	engine := &AgentEngine{
		imageDescriber: func(ctx context.Context, _ []byte, _ string) (string, error) {
			n := running.Add(1)
			defer running.Add(-1)
			for {
				old := peak.Load()
				if n <= old || peak.CompareAndSwap(old, n) {
					break
				}
			}
			if _, ok := ctx.Deadline(); !ok {
				return "", errors.New("missing per-image deadline")
			}
			<-release
			return "ok", nil
		},
	}
	dataURI := "data:image/png;base64," + base64.StdEncoding.EncodeToString([]byte{0x89})
	uris := make([]string, 6)
	for i := range uris {
		uris[i] = dataURI
	}

	done := make(chan []string)
	go func() { done <- engine.describeImages(context.Background(), uris, nil) }()
	deadline := time.After(5 * time.Second)
	for peak.Load() < toolImageDescribeConcurrency {
		select {
		case <-deadline:
			t.Fatalf("peak concurrency = %d; want %d", peak.Load(), toolImageDescribeConcurrency)
		case <-time.After(5 * time.Millisecond):
		}
	}
	close(release)
	descriptions := <-done
	if peak.Load() != toolImageDescribeConcurrency {
		t.Errorf("peak concurrency = %d; want %d", peak.Load(), toolImageDescribeConcurrency)
	}
	for i, desc := range descriptions {
		if desc != "ok" {
			t.Errorf("description %d = %q; want ok", i, desc)
		}
	}
}
