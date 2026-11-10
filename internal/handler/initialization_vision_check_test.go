package handler

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/utils/ollama"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/utils"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type remoteVisionCheckUpstreamRequest struct {
	Path     string
	Model    string `json:"model"`
	Messages []struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
		Images  [][]byte        `json:"images"`
	} `json:"messages"`
}

func newRemoteVisionCheckGateway(
	t *testing.T, status int, response string,
) (*httptest.Server, <-chan remoteVisionCheckUpstreamRequest) {
	t.Helper()
	requests := make(chan remoteVisionCheckUpstreamRequest, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusOK)
			return
		}
		request := remoteVisionCheckUpstreamRequest{Path: r.URL.Path}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode upstream request: %v", err)
			http.Error(w, "invalid test request", http.StatusBadRequest)
			return
		}
		// Keep the first request when the adapter retries.
		select {
		case requests <- request:
		default:
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(response))
	}))
	t.Cleanup(server.Close)
	return server, requests
}

func allowRemoteVisionCheckLoopback(t *testing.T) {
	t.Helper()
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")
	utils.ResetSSRFWhitelistForTest()
	t.Cleanup(utils.ResetSSRFWhitelistForTest)
}

func runRemoteVisionCheck(t *testing.T, handler *InitializationHandler, payload map[string]any) bool {
	t.Helper()
	body, err := json.Marshal(payload)
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodPost, "/initialization/remote/check", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	ctx := context.WithValue(req.Context(), types.TenantInfoContextKey, &types.Tenant{ID: 42})
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = req.WithContext(ctx)

	handler.CheckRemoteModel(c)

	require.Empty(t, c.Errors)
	require.Equal(t, http.StatusOK, recorder.Code)
	var response struct {
		Success bool `json:"success"`
		Data    *struct {
			Available bool `json:"available"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
	require.True(t, response.Success)
	require.NotNil(t, response.Data)
	return response.Data.Available
}

func assertRemoteVisionCheckImage(t *testing.T, requests <-chan remoteVisionCheckUpstreamRequest, path string) {
	t.Helper()
	var request remoteVisionCheckUpstreamRequest
	select {
	case request = <-requests:
	default:
		t.Fatal("the model provider did not receive a request")
	}
	require.Equal(t, path, request.Path)
	require.Equal(t, "vision-model", request.Model)
	require.Len(t, request.Messages, 1)
	message := request.Messages[0]
	require.Equal(t, "user", message.Role)
	var imageData []byte
	if path == "/api/chat" {
		require.Len(t, message.Images, 1)
		imageData = message.Images[0]
	} else {
		var parts []struct {
			Type     string `json:"type"`
			ImageURL struct {
				URL string `json:"url"`
			} `json:"image_url"`
		}
		require.NoError(t, json.Unmarshal(message.Content, &parts))
		require.Len(t, parts, 2)
		require.Equal(t, "text", parts[0].Type)
		require.Equal(t, "image_url", parts[1].Type)
		encoded, found := strings.CutPrefix(parts[1].ImageURL.URL, "data:image/png;base64,")
		require.True(t, found, "expected an inline PNG image")
		var err error
		imageData, err = base64.StdEncoding.DecodeString(encoded)
		require.NoError(t, err)
	}
	img, err := png.Decode(bytes.NewReader(imageData))
	require.NoError(t, err)
	require.Positive(t, img.Bounds().Dx())
	require.Positive(t, img.Bounds().Dy())
}

func TestCheckRemoteModelVisionSendsImageAndRequiresCompleteReply(t *testing.T) {
	allowRemoteVisionCheckLoopback(t)
	for _, tc := range []struct {
		name      string
		status    int
		response  string
		available bool
	}{
		{
			"caption", http.StatusOK,
			`{"choices":[{"message":{"role":"assistant","content":"Red square"},"finish_reason":"stop"}]}`, true,
		},
		{
			"blank", http.StatusOK,
			`{"choices":[{"message":{"role":"assistant","content":"  "},"finish_reason":"stop"}]}`, false,
		},
		{
			"truncated caption", http.StatusOK,
			`{"choices":[{"message":{"role":"assistant","content":"A red"},"finish_reason":"length"}]}`, false,
		},
		{"image rejected", http.StatusBadRequest, `{"error":{"message":"Image inputs are unsupported"}}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gateway, requests := newRemoteVisionCheckGateway(t, tc.status, tc.response)
			available := runRemoteVisionCheck(t, &InitializationHandler{}, map[string]any{
				"modelName": "vision-model",
				"modelType": types.ModelTypeVLLM,
				"provider":  "generic",
				"baseUrl":   gateway.URL + "/v1",
				"apiKey":    "test-key",
			})
			require.Equal(t, tc.available, available)
			assertRemoteVisionCheckImage(t, requests, "/v1/chat/completions")
		})
	}
}

func TestCheckRemoteModelVisionPreservesStoredOllamaInterface(t *testing.T) {
	t.Setenv("SSRF_WHITELIST", "")
	t.Setenv("SSRF_WHITELIST_EXTRA", "")
	utils.ResetSSRFWhitelistForTest()
	t.Cleanup(utils.ResetSSRFWhitelistForTest)
	for _, tc := range []struct {
		name          string
		interfaceType string
		path          string
		ollamaReply   string
		available     bool
	}{
		{
			"stored interface", "", "/api/chat",
			`{"message":{"role":"assistant","content":"Red square"},"done":true,"done_reason":"stop"}`, true,
		},
		{
			"stored interface rejects truncated reply", "", "/api/chat",
			`{"message":{"role":"assistant","content":"A red"},"done":true,"done_reason":"length"}`, false,
		},
		{"explicit override", "openai", "/v1/chat/completions", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			native, nativeRequests := newRemoteVisionCheckGateway(t, http.StatusOK, tc.ollamaReply)
			openai, openAIRequests := newRemoteVisionCheckGateway(t, http.StatusOK,
				`{"choices":[{"message":{"role":"assistant","content":"Red square"},"finish_reason":"stop"}]}`)
			if tc.interfaceType == "" {
				require.Error(t, utils.ValidateURLForSSRF(openai.URL))
			} else {
				allowRemoteVisionCheckLoopback(t)
			}
			t.Setenv("OLLAMA_BASE_URL", native.URL)
			service, err := ollama.GetOllamaService()
			require.NoError(t, err)
			stored := &types.Model{
				ID:     "saved-vision",
				Name:   "vision-model",
				Type:   types.ModelTypeVLLM,
				Source: types.ModelSourceRemote,
				Parameters: types.ModelParameters{
					InterfaceType: "ollama",
					BaseURL:       openai.URL + "/v1",
					Provider:      "generic",
				},
			}
			handler := &InitializationHandler{
				ollamaService: service,
				modelService: &stubFillSecretsModelService{
					getModelByID: func(_ context.Context, id string) (*types.Model, error) {
						require.Equal(t, stored.ID, id)
						return stored, nil
					},
				},
			}
			// Complete draft credentials and metadata must not skip the stored interface lookup.
			payload := map[string]any{
				"modelId":     stored.ID,
				"modelName":   stored.Name,
				"modelType":   types.ModelTypeVLLM,
				"provider":    stored.Parameters.Provider,
				"baseUrl":     stored.Parameters.BaseURL,
				"apiKey":      "test-key",
				"appSecret":   "unused-test-secret",
				"extraConfig": map[string]string{},
				"spec":        &types.ModelSpecOverride{},
			}
			if tc.interfaceType != "" {
				payload["interfaceType"] = tc.interfaceType
			}
			require.Equal(t, tc.available, runRemoteVisionCheck(t, handler, payload))
			if tc.path == "/api/chat" {
				assertRemoteVisionCheckImage(t, nativeRequests, tc.path)
				require.Empty(t, openAIRequests)
			} else {
				assertRemoteVisionCheckImage(t, openAIRequests, tc.path)
				require.Empty(t, nativeRequests)
			}
		})
	}
}

func TestCheckRemoteModelDefaultsToTextProbe(t *testing.T) {
	allowRemoteVisionCheckLoopback(t)
	gateway, requests := newRemoteVisionCheckGateway(t, http.StatusOK,
		`{"choices":[{"message":{"role":"assistant","content":"OK"},"finish_reason":"stop"}]}`)
	require.True(t, runRemoteVisionCheck(t, &InitializationHandler{}, map[string]any{
		"modelName": "chat-model",
		"provider":  "generic",
		"baseUrl":   gateway.URL + "/v1",
		"apiKey":    "test-key",
	}))
	require.Len(t, requests, 1)
	request := <-requests
	require.Equal(t, "/v1/chat/completions", request.Path)
	require.Len(t, request.Messages, 1)
	var prompt string
	require.NoError(t, json.Unmarshal(request.Messages[0].Content, &prompt))
	require.NotEmpty(t, prompt)
}
