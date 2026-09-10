package adapters

// weknoracloud.go — WeKnoraCloud chat facet (design §6.2/§6.8). Behavior port
// of v1 chat/provider.go weKnoraCloudProvider (endpoint + ForceRawHTTP +
// TransformMessages multi-content downgrade + HMAC request signing) and the
// chat-side of v1 vlm/weknoracloud.go (same signed /api/v1/chat/completions
// route; the VLM facet rides Chat via InputModalities).
import (
	"fmt"
	"strings"

	"github.com/Tencent/WeKnora/internal/models/invoke"
	"github.com/Tencent/WeKnora/internal/models/provider"
	modelutils "github.com/Tencent/WeKnora/internal/models/utils"
	"github.com/google/uuid"
	"github.com/sashabaranov/go-openai"
)

// weKnoraCloudAdapter composes the openai fallback (request funnel, parse,
// stream bridge) with the WeKnoraCloud deltas: signed endpoint, multi-content
// downgrade, body-HMAC auth.
type weKnoraCloudAdapter struct {
	openaiAdapter
}

// compile-time lock #1 (design §6.2).
var _ invoke.ChatAdapter = (*weKnoraCloudAdapter)(nil)

func newWeKnoraCloudAdapter() *weKnoraCloudAdapter {
	return &weKnoraCloudAdapter{openaiAdapter{
		name: provider.ProviderWeKnoraCloud,
		spec: openaiVendorSpec{
			// v1 weKnoraCloudProvider: ForceRawHTTP + multi-content downgrade.
			forceRaw:  true,
			sign:      true,
			transform: transformWeKnoraCloudMessages,
		},
		caps: chatCapsFor(provider.ProviderWeKnoraCloud),
	}}
}

// BuildChatRequest builds the openai-form body (downgraded messages, map-form
// roundtrip via spec.forceRaw) and then signs it: the HMAC depends on the
// final body bytes, so it must happen inside BuildChatRequest (design §6.2).
func (a *weKnoraCloudAdapter) BuildChatRequest(
	ep invoke.Endpoint, model string, opts *invoke.ChatOptions,
) (*invoke.Request, error) {
	req, err := a.openaiAdapter.BuildChatRequest(ep, model, opts)
	if err != nil {
		return nil, err
	}
	// v1 weKnoraCloudProvider.Auth (provider.go:93-99): sign over the final
	// body bytes with AppID/AppSecret (modelutils.Sign).
	requestID := uuid.NewString()
	for k, v := range modelutils.Sign(ep.Credentials.AppID, ep.Credentials.AppSecret, requestID, string(req.Body)) {
		req.Header.Set(k, v)
	}
	// Signature/auth-critical headers the entry must not let user custom
	// headers override (design §6.4 protected-header rule).
	req.ProtectedHeaders = append(req.ProtectedHeaders,
		"X-Appid", "X-Api-Key", "X-Request-Id", "X-Timestamp", "X-Nonce", "X-Signature")
	return req, nil
}

// transformWeKnoraCloudMessages ports weKnoraCloudProvider.TransformMessages
// (provider.go:103-120): downgrades MultiContent to newline-joined plain text
// while preserving tool_calls / tool_call_id / name so the function-calling
// protocol keeps working.
func transformWeKnoraCloudMessages(messages []openai.ChatCompletionMessage) []openai.ChatCompletionMessage {
	result := make([]openai.ChatCompletionMessage, 0, len(messages))
	for _, m := range messages {
		msg := m
		if msg.Content == "" && len(msg.MultiContent) > 0 {
			var textParts []string
			for _, part := range msg.MultiContent {
				if part.Type == openai.ChatMessagePartTypeText && part.Text != "" {
					textParts = append(textParts, part.Text)
				}
			}
			msg.Content = strings.Join(textParts, "\n")
			msg.MultiContent = nil
		}
		result = append(result, msg)
	}
	return result
}

func init() {
	if err := invoke.Default.Register(newWeKnoraCloudAdapter()); err != nil {
		panic(fmt.Sprintf("invoke/adapters: register weknoracloud adapter: %v", err))
	}
}
