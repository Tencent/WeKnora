package providers_test

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/models/providers"
	"github.com/Tencent/WeKnora/internal/models/vlm"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

// TestOCIEnterpriseAILive calls a real OCI Enterprise AI endpoint. Skipped
// unless OCI_ENTERPRISE_AI_TEST_API_KEY is set, e.g.
//
//	OCI_ENTERPRISE_AI_TEST_ENDPOINT=https://inference.generativeai.us-chicago-1.oci.oraclecloud.com/openai/v1 \
//	OCI_ENTERPRISE_AI_TEST_API_KEY=... OCI_ENTERPRISE_AI_TEST_PROJECT_ID=ocid1.generativeaiproject.oc1.... \
//	OCI_ENTERPRISE_AI_TEST_MODEL=xai.grok-4.3 [OCI_ENTERPRISE_AI_TEST_VLM_MODEL=xai.grok-4.3] \
//	go test -run TestOCIEnterpriseAILive ./internal/models/providers/
func TestOCIEnterpriseAILive(t *testing.T) {
	apiKey := os.Getenv("OCI_ENTERPRISE_AI_TEST_API_KEY")
	if apiKey == "" {
		t.Skip("OCI_ENTERPRISE_AI_TEST_API_KEY not set")
	}
	baseURL := os.Getenv("OCI_ENTERPRISE_AI_TEST_ENDPOINT")
	extra := map[string]string{providers.OCIEnterpriseAIProjectKey: os.Getenv("OCI_ENTERPRISE_AI_TEST_PROJECT_ID")}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	c, err := chat.NewRemoteChat(&chat.ChatConfig{
		Source: types.ModelSourceRemote, BaseURL: baseURL, APIKey: apiKey,
		ModelName: os.Getenv("OCI_ENTERPRISE_AI_TEST_MODEL"), Provider: providers.OCIEnterpriseAIID,
		ExtraConfig: extra,
	})
	require.NoError(t, err)
	resp, err := c.Chat(ctx, []chat.Message{
		{Role: "system", Content: "Answer with one word."},
		{Role: "user", Content: "What is the capital of Japan?"},
	}, &chat.ChatOptions{})
	require.NoError(t, err)
	require.Contains(t, strings.ToLower(resp.Content), "tokyo")

	vlmModel := os.Getenv("OCI_ENTERPRISE_AI_TEST_VLM_MODEL")
	if vlmModel == "" {
		return
	}
	v, err := vlm.NewVLM(&vlm.Config{
		Source: types.ModelSourceRemote, BaseURL: baseURL, APIKey: apiKey, ModelName: vlmModel,
		Provider: providers.OCIEnterpriseAIID,
		Extra:    map[string]any{providers.OCIEnterpriseAIProjectKey: extra[providers.OCIEnterpriseAIProjectKey]},
	}, nil)
	require.NoError(t, err)
	img := image.NewRGBA(image.Rect(0, 0, 64, 64))
	for x := 0; x < 64; x++ {
		for y := 0; y < 64; y++ {
			img.Set(x, y, color.RGBA{R: 220, A: 255})
		}
	}
	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, img))
	answer, err := v.Predict(ctx, [][]byte{buf.Bytes()}, "What color fills this image? Answer with one English word.")
	require.NoError(t, err)
	require.Contains(t, strings.ToLower(answer), "red")
}
