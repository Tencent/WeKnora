package asr

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	openai "github.com/sashabaranov/go-openai"
)

// TestASRLanguageCompatibility 验证 SDK 接受不同语言元数据，且不改变正文、时间戳或上传请求。
func TestASRLanguageCompatibility(t *testing.T) {
	withASRSSRFWhitelist(t, "127.0.0.1")
	for _, tc := range []struct {
		name     string // 用例名称。
		metadata string // 响应中的语言字段，可缺省。
		want     string // SDK 解码后的单一语言；空值表示未提供。
	}{
		{"string", `,"language":"English"`, "English"},
		{"single language", `,"language":["English"]`, "English"},
		{"multiple languages", `,"language":["Chinese","English"]`, ""},
		{"empty array", `,"language":[]`, ""},
		{"null", `,"language":null`, ""},
		{"missing", ``, ""},
		{"number", `,"language":123`, ""},
		{"object", `,"language":{"detected":"English"}`, ""},
		{"boolean", `,"language":true`, ""},
		{"non-string element", `,"language":[123]`, ""},
		{"null element", `,"language":[null]`, ""},
		{"nested array", `,"language":[["English"]]`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.URL.Path != "/v1/audio/transcriptions" {
					t.Errorf("unexpected endpoint: %s %s", r.Method, r.URL.Path)
				}
				if err := r.ParseMultipartForm(1 << 20); err != nil {
					t.Error(err)
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				defer func() {
					if err := r.MultipartForm.RemoveAll(); err != nil {
						t.Error(err)
					}
				}()
				if r.FormValue("model") != "qwen-asr" || r.FormValue("response_format") != "verbose_json" ||
					r.FormValue("language") != "en" {
					t.Errorf("request fields changed: %v", r.Form)
				}
				if r.Header.Get("Authorization") != "Bearer test-key" || r.Header.Get("X-ASR-Test") != "forwarded" {
					t.Error("authentication or custom header lost")
				}
				file, header, err := r.FormFile("file")
				if err != nil {
					t.Error(err)
					return
				}
				defer func() {
					if err := file.Close(); err != nil {
						t.Error(err)
					}
				}()
				data, err := io.ReadAll(file)
				if err != nil || string(data) != "audio fixture" || header.Filename != "mixed.wav" {
					t.Error("audio or filename changed")
				}
				w.Header().Set("Content-Type", "application/json")
				_, err = io.WriteString(w, `{"text":"今天讨论 release plan。","duration":2.5,`+
					`"segments":[{"start":0.5,"end":2.5,"text":"今天讨论 release plan。"}]`+tc.metadata+`}`)
				if err != nil {
					t.Error(err)
				}
			}))
			defer server.Close()
			model, err := NewOpenAIASR(&Config{
				BaseURL: server.URL + "/v1", ModelName: "qwen-asr", APIKey: "test-key",
				CustomHeaders: map[string]string{"X-ASR-Test": "forwarded"},
			})
			if err != nil {
				t.Fatal(err)
			}
			result, err := model.client.CreateTranscription(context.Background(), openai.AudioRequest{
				Model: "qwen-asr", FilePath: "mixed.wav", Reader: strings.NewReader("audio fixture"),
				Format: openai.AudioResponseFormatVerboseJSON, Language: "en",
			})
			if err != nil {
				t.Fatal(err)
			}
			if result.Language != tc.want || result.Text != "今天讨论 release plan。" || result.Duration != 2.5 {
				t.Fatalf("unexpected SDK result: %+v", result)
			}
			if len(result.Segments) != 1 || result.Segments[0].Start != 0.5 ||
				result.Segments[0].End != 2.5 || result.Segments[0].Text != result.Text {
				t.Fatalf("segments changed: %+v", result.Segments)
			}
		})
	}
}

// TestTranscribeMLXResponse 回放实际 MLX Audio 0.5.4 响应，验证业务转写入口保留文字和分段。
func TestTranscribeMLXResponse(t *testing.T) {
	withASRSSRFWhitelist(t, "127.0.0.1")
	body, err := os.ReadFile("testdata/mlx_audio_verbose.json")
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		if _, err := w.Write(body); err != nil {
			t.Error(err)
		}
	}))
	defer server.Close()
	model, err := NewOpenAIASR(&Config{BaseURL: server.URL + "/v1", ModelName: "qwen-asr"})
	if err != nil {
		t.Fatal(err)
	}
	result, err := model.Transcribe(context.Background(), []byte("audio fixture"), "asr_en.wav")
	if err != nil {
		t.Fatal(err)
	}
	want := "Uh huh. Oh yeah, yeah. He wasn't even that big when I started listening to him, " +
		"but and his solo music didn't do overly well, but he did very well when he started writing for other people."
	if calls != 1 || result.Text != want || len(result.Segments) != 1 ||
		result.Segments[0].Text != want || result.Segments[0].Start != 0 || result.Segments[0].End != 15.05125 {
		t.Fatalf("unexpected transcription: calls=%d result=%+v", calls, result)
	}
}

// TestASRLanguageErrors 验证适配不吞掉 HTTP、JSON 或业务字段类型错误，也不会再次调用模型。
func TestASRLanguageErrors(t *testing.T) {
	withASRSSRFWhitelist(t, "127.0.0.1")
	for _, tc := range []struct {
		name   string // 失败场景。
		status int    // 上游 HTTP 状态码。
		body   string // 上游响应体。
	}{
		{"HTTP error", 400, `{"error":{"message":"unsupported response_format"}}`},
		{"invalid JSON", 200, `{"text":`},
		{"trailing JSON", 200, `{"text":"ok","language":["English"]} {}`},
		{"invalid text", 200, `{"text":123,"language":["English"]}`},
		{"invalid segment", 200, `{"text":"ok","segments":[{"start":"bad"}],"language":["English"]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls++
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				if _, err := io.WriteString(w, tc.body); err != nil {
					t.Error(err)
				}
			}))
			defer server.Close()
			model, err := NewOpenAIASR(&Config{BaseURL: server.URL + "/v1"})
			if err != nil {
				t.Fatal(err)
			}
			result, err := model.Transcribe(context.Background(), []byte("audio"), "test.wav")
			if err == nil || result != nil || calls != 1 {
				t.Fatalf("error swallowed or retried: calls=%d result=%+v err=%v", calls, result, err)
			}
			if tc.status == 400 {
				var apiErr *openai.APIError
				if !errors.As(err, &apiErr) || apiErr.HTTPStatusCode != 400 ||
					apiErr.Message != "unsupported response_format" {
					t.Fatalf("SDK error changed: %v", err)
				}
			}
		})
	}
}

// TestNormalizeASRLanguagePreservesFields 验证未改写的字段不因通用 JSON 数字转换而丢失精度。
func TestNormalizeASRLanguagePreservesFields(t *testing.T) {
	body := []byte(`{"text":"I love you","language":["English"],"provider_id":9007199254740993}`)
	result, err := normalizeASRLanguage(body)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(result, []byte(`"provider_id":9007199254740993`)) ||
		!bytes.Contains(result, []byte(`"text":"I love you"`)) {
		t.Fatalf("unrelated fields changed: %s", result)
	}
}

// asrResponseTransport 为响应体生命周期检查提供固定响应。
type asrResponseTransport struct {
	body io.ReadCloser // 由被测适配层读取并关闭的响应体。
}

// RoundTrip 返回固定的成功响应，避免生命周期检查依赖网络。
func (t asrResponseTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: t.body}, nil
}

// asrTrackedBody 记录上游响应体是否关闭，并支持注入读取失败。
type asrTrackedBody struct {
	reader io.Reader // 响应内容。
	err    error     // 非空时模拟上游读取失败。
	closed bool      // 标记适配层是否释放上游响应体。
}

// Read 读取响应内容或返回预设错误。
func (b *asrTrackedBody) Read(p []byte) (int, error) {
	if b.err != nil {
		return 0, b.err
	}
	return b.reader.Read(p)
}

// Close 记录响应体关闭操作。
func (b *asrTrackedBody) Close() error {
	b.closed = true
	return nil
}

// TestASRLanguageResponseBody 验证成功、格式错误和读取失败均释放上游响应体，且保留取消错误。
func TestASRLanguageResponseBody(t *testing.T) {
	for _, tc := range []struct {
		name string // 场景名称。
		body string // 上游响应内容。
		err  error  // 模拟读取错误。
	}{
		{"success", `{"text":"hello","language":["English"]}`, nil},
		{"malformed", `{"text":`, nil},
		{"canceled", "", context.Canceled},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := &asrTrackedBody{reader: strings.NewReader(tc.body), err: tc.err}
			transport := &asrLanguageTransport{base: asrResponseTransport{body: body}}
			req, err := http.NewRequestWithContext(context.Background(), http.MethodPost,
				"https://asr.example/v1/audio/transcriptions", nil)
			if err != nil {
				t.Fatal(err)
			}
			resp, err := transport.RoundTrip(req)
			if !body.closed {
				t.Error("upstream response body was not closed")
			}
			if tc.name != "success" {
				if err == nil || resp != nil {
					t.Fatalf("failure swallowed: resp=%v err=%v", resp, err)
				}
				if tc.err != nil && !errors.Is(err, tc.err) {
					t.Fatalf("cancellation cause lost: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := resp.Body.Close(); err != nil {
					t.Error(err)
				}
			}()
			data, err := io.ReadAll(resp.Body)
			if err != nil || resp.ContentLength != int64(len(data)) ||
				!bytes.Contains(data, []byte(`"language":"English"`)) {
				t.Fatalf("replacement response invalid: data=%s length=%d err=%v", data, resp.ContentLength, err)
			}
		})
	}
}
