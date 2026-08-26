package tongyi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/custom/config"
)

func TestCreateTaskRejectsEmptyFileURL(t *testing.T) {
	client := New(config.TongyiConfig{
		AccessKeyID:     "access-id",
		AccessKeySecret: "access-secret",
		AppKey:          "app-key",
		Endpoint:        "https://tingwu.example.test",
	})

	_, err := client.CreateTask(context.Background(), CreateTaskRequest{})
	if err == nil || !strings.Contains(err.Error(), "file url is empty") {
		t.Fatalf("CreateTask() error = %v, want empty file url error", err)
	}
}

func TestCreateTaskRejectsEmptyTaskID(t *testing.T) {
	client := New(config.TongyiConfig{
		AccessKeyID:     "access-id",
		AccessKeySecret: "access-secret",
		AppKey:          "app-key",
		Endpoint:        "https://tingwu.example.test",
	})
	client.http = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return jsonResponse(`{"Code":"0","Data":{"TaskStatus":"SUBMITTED"}}`), nil
	})}

	_, err := client.CreateTask(context.Background(), CreateTaskRequest{FileURL: "https://cdn.example.test/video.mp4"})
	if err == nil || !strings.Contains(err.Error(), "empty task id") {
		t.Fatalf("CreateTask() error = %v, want empty task id error", err)
	}
}

func TestValidateSourceFileRejectsLocalhost(t *testing.T) {
	client := New(config.TongyiConfig{Endpoint: "https://tingwu.example.test"})
	client.http = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("source check should not make a request for localhost")
		return nil, nil
	})}

	err := client.ValidateSourceFile(context.Background(), "http://localhost:9000/video.mp4")
	if err == nil || !strings.Contains(err.Error(), "not publicly reachable") {
		t.Fatalf("ValidateSourceFile() error = %v, want public reachability error", err)
	}
}

func TestValidateSourceFileRejectsNonSuccessResponse(t *testing.T) {
	client := New(config.TongyiConfig{Endpoint: "https://tingwu.example.test"})
	client.http = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodHead {
			t.Fatalf("request method = %s, want HEAD", req.Method)
		}
		return &http.Response{
			StatusCode: http.StatusNotFound,
			Status:     "404 Not Found",
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader("")),
		}, nil
	})}

	err := client.ValidateSourceFile(context.Background(), "https://cdn.example.test/video.mp4")
	if err == nil || !strings.Contains(err.Error(), "status=404") {
		t.Fatalf("ValidateSourceFile() error = %v, want status error", err)
	}
}

func TestValidateSourceFileAcceptsNonEmptyHeadResponse(t *testing.T) {
	client := New(config.TongyiConfig{Endpoint: "https://tingwu.example.test"})
	client.http = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodHead {
			t.Fatalf("request method = %s, want HEAD", req.Method)
		}
		return &http.Response{
			StatusCode:    http.StatusOK,
			Status:        "200 OK",
			Header:        make(http.Header),
			ContentLength: 1024,
			Body:          io.NopCloser(strings.NewReader("")),
		}, nil
	})}

	if err := client.ValidateSourceFile(context.Background(), "https://cdn.example.test/video.mp4"); err != nil {
		t.Fatalf("ValidateSourceFile() error = %v, want nil", err)
	}
}

func TestDownloadResultAggregatesWordsIntoSentences(t *testing.T) {
	client := New(config.TongyiConfig{Endpoint: "https://tingwu.example.test"})
	client.http = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Path != "/transcript" {
			return nil, fmt.Errorf("unexpected path %s", req.URL.Path)
		}
		return jsonResponse(`{"Transcription":{"Paragraphs":[{"ParagraphId":"p1","SpeakerId":"spk1","Words":[{"SentenceId":1,"Start":100,"End":300,"Text":"你好"},{"SentenceId":1,"Start":300,"End":500,"Text":"世界"},{"SentenceId":2,"Start":600,"End":800,"Text":"再见"}]}]}}`), nil
	})}
	result, err := client.DownloadResult(context.Background(), mustJSON(map[string]string{"Transcription": "https://tingwu.example.test/transcript"}))
	if err != nil {
		t.Fatalf("DownloadResult() error = %v", err)
	}

	if len(result.Transcripts) != 1 || len(result.Transcripts[0].Paragraphs) != 1 {
		t.Fatalf("DownloadResult() shape = %#v", result)
	}
	sentences := result.Transcripts[0].Paragraphs[0].Sentences
	if len(sentences) != 2 {
		t.Fatalf("sentence count = %d, want 2", len(sentences))
	}
	if sentences[0].Text != "你好世界" || sentences[0].StartMs != 100 || sentences[0].EndMs != 500 {
		t.Fatalf("first sentence = %#v", sentences[0])
	}
}

func mustJSON(value any) string {
	body, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return string(body)
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func jsonResponse(body string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Status:     "200 OK",
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}
