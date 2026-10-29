package line

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/Tencent/WeKnora/internal/im"
	"github.com/Tencent/WeKnora/internal/logger"
	secutils "github.com/Tencent/WeKnora/internal/utils"
)

// Adapter implements im.Adapter for the LINE Messaging API (webhook mode).
type Adapter struct {
	channelAccessToken string
	channelSecret      string
	resetKeyword       string
	// enableFlexMessage opts the adapter into structured Flex Message output
	// (structured layout with a title and source tag). When false, replies are
	// emitted as plain text messages.
	enableFlexMessage bool
}

// Compile-time check that Adapter satisfies the im.Adapter interface.
var _ im.Adapter = (*Adapter)(nil)

// NewWebhookAdapter creates a LINE adapter for webhook delivery.
//
// channelAccessToken is the bot messaging token; channelSecret is the channel
// secret used to verify incoming webhook signatures (x-line-signature).
//
// resetKeyword is the per-channel command (e.g. "/reset", "/clear", "/new",
// "/restart") that clears the current session context. When empty, no custom
// keyword is matched and only the platform-level mechanism applies.
//
// The structured Flex Message output stays disabled; use
// NewWebhookAdapterWithFlex to opt into it.
func NewWebhookAdapter(channelAccessToken, channelSecret, resetKeyword string) *Adapter {
	return NewWebhookAdapterWithFlex(channelAccessToken, channelSecret, resetKeyword, false)
}

// NewWebhookAdapterWithFlex is like NewWebhookAdapter but enables structured
// Flex Message output when enableFlexMessage is true. When false it emits plain
// text messages, matching the default adapter behaviour.
func NewWebhookAdapterWithFlex(
	channelAccessToken, channelSecret, resetKeyword string,
	enableFlexMessage bool,
) *Adapter {
	return &Adapter{
		channelAccessToken: channelAccessToken,
		channelSecret:      channelSecret,
		resetKeyword:       strings.TrimSpace(resetKeyword),
		enableFlexMessage:  enableFlexMessage,
	}
}

// isReset reports whether content matches this channel's configured reset
// keyword. Matching is exact after trimming and case-folding, so a keyword of
// "/reset" only fires when the user sends exactly "/reset" (nothing more,
// nothing less) — just like the other slash commands.
func (a *Adapter) isReset(content string) bool {
	if a.resetKeyword == "" {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(content), a.resetKeyword)
}

func (a *Adapter) Platform() im.Platform {
	return im.PlatformLine
}

// verifySignature computes the expected LINE webhook signature: Base64-encoded
// HMAC-SHA256 of the raw request body keyed by the channel secret.
func verifySignature(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

// VerifyCallback verifies the incoming webhook signature (x-line-signature).
func (a *Adapter) VerifyCallback(c *gin.Context) error {
	if a.channelSecret == "" {
		return fmt.Errorf("line channel secret is required")
	}
	if a.channelAccessToken == "" {
		return fmt.Errorf("line channel access token is required")
	}

	im.LimitWebhookBody(c)
	bodyBytes, err := io.ReadAll(c.Request.Body)
	if err != nil {
		return fmt.Errorf("read body: %w", err)
	}
	c.Request.Body = io.NopCloser(bytes.NewReader(bodyBytes))

	expected := verifySignature(a.channelSecret, bodyBytes)
	provided := c.GetHeader("X-Line-Signature")
	if provided == "" {
		return fmt.Errorf("missing x-line-signature header")
	}
	if subtle.ConstantTimeCompare([]byte(provided), []byte(expected)) != 1 {
		return fmt.Errorf("line signature verification failed")
	}
	return nil
}

// parseCallback decodes the LINE webhook payload.
func parseCallback(bodyBytes []byte) (*webhookPayload, error) {
	var payload webhookPayload
	if err := json.Unmarshal(bodyBytes, &payload); err != nil {
		return nil, err
	}
	return &payload, nil
}

// ParseCallback decodes the LINE webhook payload into a unified IncomingMessage.
func (a *Adapter) ParseCallback(c *gin.Context) (*im.IncomingMessage, error) {
	im.LimitWebhookBody(c)
	bodyBytes, err := io.ReadAll(c.Request.Body)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}
	c.Request.Body = io.NopCloser(bytes.NewReader(bodyBytes))

	payload, err := parseCallback(bodyBytes)
	if err != nil {
		return nil, fmt.Errorf("parse line payload: %w", err)
	}

	for i := range payload.Events {
		ev := payload.Events[i]
		if ev.Type != "message" || ev.Message == nil {
			// Non-message events (follow/unfollow/left) are handled by the
			// platform dashboard; keep scanning for a text message.
			continue
		}
		if strings.ToLower(ev.Message.Type) != "text" {
			// Text-only in this phase; other message kinds are skipped.
			// LINE sends the type lowercase ("text"); ToLower keeps it robust
			// against case drift so real text messages are not silently dropped.
			continue
		}
		if ev.Source.Type == "group" || ev.Source.Type == "room" {
			// Ignore group/room chatter unless the bot is @-mentioned, so the
			// pipeline is not disturbed by everyday group conversation.
			if !wasBotMentioned(ev.Message) {
				continue
			}
		}
		incoming := buildIncoming(ev, ev.Message)
		incoming.Extra = map[string]string{
			"replyToken": ev.ReplyToken,
		}
		// Reset keyword: flag it so the service routes this through the same
		// session-clear action as "/clear". Matching is case-insensitive and
		// exact (see isReset), so stray text never triggers a reset.
		if a.isReset(incoming.Content) {
			incoming.Extra["reset"] = "true"
		}
		return incoming, nil
	}
	return nil, nil
}

// wasBotMentioned reports whether a LINE message @-mentions the bot. It is true
// when any mention target is the bot (type "bot" or flagged isSelf), or when the
// message text begins with an "@..." mention as a fallback heuristic.
func wasBotMentioned(msg *webhookMessage) bool {
	if msg == nil {
		return false
	}
	if msg.Mention != nil {
		for _, m := range msg.Mention.Mentionees {
			if m.Type == "bot" || m.IsSelf {
				return true
			}
		}
	}
	return strings.HasPrefix(msg.Text, "@")
}

// stripBotMention removes a leading "@botname" token from the message text so
// only the clean question reaches the answer pipeline. It is a no-op when the
// text does not begin with an "@..." mention.
func stripBotMention(text string) string {
	s := strings.TrimSpace(text)
	if !strings.HasPrefix(s, "@") {
		return s
	}
	rest := s[1:]
	if idx := strings.IndexAny(rest, " \t\r\n"); idx >= 0 {
		rest = rest[idx:]
	} else {
		rest = ""
	}
	return strings.TrimSpace(rest)
}

// buildIncoming maps a single LINE message event to the unified model.
func buildIncoming(ev webhookEvent, msg *webhookMessage) *im.IncomingMessage {
	chatType := im.ChatTypeDirect
	chatID := ""
	switch ev.Source.Type {
	case "group":
		chatType = im.ChatTypeGroup
		chatID = ev.Source.GroupID
	case "room":
		chatType = im.ChatTypeGroup
		chatID = ev.Source.RoomID
	}

	content := msg.Text
	// For group/room chatter we only deliver messages that mention the bot
	// (enforced in ParseCallback). Strip the leading "@botname" so only the
	// clean question reaches the answer pipeline.
	if chatType == im.ChatTypeGroup {
		content = stripBotMention(content)
	}

	return &im.IncomingMessage{
		Platform:  im.PlatformLine,
		ChatType:  chatType,
		UserID:    ev.Source.UserID,
		ChatID:    chatID,
		MessageID: msg.ID,
		Content:   strings.TrimSpace(content),
	}
}

// resolveTarget returns the push target (chat id or user id) for fallback send.
func resolveTarget(incoming *im.IncomingMessage) string {
	if incoming.ChatID != "" {
		return incoming.ChatID
	}
	return incoming.UserID
}

// sendReply delivers a reply via the reply token.
func (a *Adapter) sendReply(ctx context.Context, replyToken string, messages []map[string]interface{}) error {
	body := map[string]interface{}{
		"replyToken": replyToken,
		"messages":   messages,
	}
	return a.callAPI(ctx, "https://api.line.me/v2/bot/message/reply", body)
}

// sendPush delivers a message via the push API (fallback when the reply token
// is expired or unavailable).
func (a *Adapter) sendPush(ctx context.Context, target string, messages []map[string]interface{}) error {
	body := map[string]interface{}{
		"to":       target,
		"messages": messages,
	}
	return a.callAPI(ctx, "https://api.line.me/v2/bot/message/push", body)
}

// shouldEmitDirectReply reports whether the LINE adapter, which is locked to
// direct (non-streaming) output, should send the given reply. Intermediate
// streaming updates are dropped; the final reply or a plain reply is emitted.
func shouldEmitDirectReply(reply *im.ReplyMessage) bool {
	if reply == nil {
		return false
	}
	return reply.IsFinal || !reply.IsStreaming
}

// flexMessages builds the LINE message payload for a reply. When enableFlex is
// true it returns a structured flex card carrying a title, the answer body, and
// a source tag; otherwise it falls back to a plain text message.
func flexMessages(text string, enableFlex bool) []map[string]interface{} {
	if enableFlex {
		return []map[string]interface{}{flexMessage(text)}
	}
	// Non-Flex path: strip raw Markdown markers via polishPlainText so the
	// chat window never shows bare **, ###, ``` and friends.
	return []map[string]interface{}{
		{
			"type": "text",
			"text": polishPlainText(text),
		},
	}
}

// polishPlainText converts a Markdown-flavoured RAG answer into clean,
// phone-friendly plain text for LINE's non-Flex reply path. Inline bold,
// headings, list markers, rules, and fenced code blocks are all normalised so
// the raw **/#/``` markers never reach the chat window.
func polishPlainText(text string) string {
	lines := strings.Split(text, "\n")
	var out []string
	var code []string
	inCode := false

	// emitCode flushes any open fenced code block; it is idempotent when the
	// fence is not open so it is safe to call at both a closing fence and EOF.
	emitCode := func() {
		if !inCode {
			return
		}
		inCode = false
		if len(code) > 0 {
			out = append(out, "[代碼區塊]")
			for _, ln := range code {
				out = append(out, strings.TrimSpace(ln))
			}
			out = append(out, "")
			code = nil
		}
	}
	// emitBlank appends a separator line while collapsing 3+ blanks to one.
	emitBlank := func() {
		if len(out) == 0 || out[len(out)-1] != "" {
			out = append(out, "")
		}
	}

	for _, raw := range lines {
		trimmed := strings.TrimSpace(raw)
		switch {
		case strings.HasPrefix(trimmed, "```"):
			// Toggle the code-fence state; emit any block being closed.
			if inCode {
				emitCode()
			} else {
				inCode = true
				code = nil
			}
		case inCode:
			code = append(code, raw)
		case trimmed == "":
			emitBlank()
		case isPolishedDivider(trimmed):
			out = append(out, dividerRule())
			emitBlank()
		case isPolishedHeader(trimmed):
			emitBlank()
			out = append(out, "📌 "+polishHeaderBody(trimmed))
			emitBlank()
		case isPolishedListItem(trimmed):
			body := strings.TrimPrefix(strings.TrimPrefix(trimmed, "- "), "* ")
			out = append(out, "• "+polishInline(body))
		default:
			out = append(out, polishInline(trimmed))
		}
	}
	emitCode()

	return strings.Trim(strings.Join(out, "\n"), "\n")
}

// isPolishedDivider reports whether s is a standalone rule of only '-' or '*'.
func isPolishedDivider(s string) bool {
	if len(s) < 3 {
		return false
	}
	for _, r := range s {
		if r != '-' && r != '*' {
			return false
		}
	}
	return true
}

// dividerRule renders a phone-friendly horizontal rule.
func dividerRule() string { return strings.Repeat("─", 20) }

// isPolishedHeader reports whether s opens a Markdown heading (leading '#').
func isPolishedHeader(s string) bool {
	n := 0
	for n < len(s) && s[n] == '#' {
		n++
	}
	return n > 0 && n < len(s)
}

// polishHeaderBody strips leading '#' markers and inlines the title body.
func polishHeaderBody(s string) string {
	n := 0
	for n < len(s) && s[n] == '#' {
		n++
	}
	return polishInline(strings.TrimSpace(s[n:]))
}

// isPolishedListItem reports whether s opens an unordered list item (- or *).
func isPolishedListItem(s string) bool {
	return strings.HasPrefix(s, "- ") || strings.HasPrefix(s, "* ")
}

// boldRe matches a **span** so inline bold can be lifted into brackets.
var boldRe = regexp.MustCompile(`\*\*(.+?)\*\*`)

// polishInline normalises inline Markdown: **bold** lifts into 【brackets】
// (LINE plain text has no per-span weight) and stray `code` backticks drop out.
func polishInline(s string) string {
	s = boldRe.ReplaceAllString(s, "【$1】")
	return strings.ReplaceAll(s, "`", "")
}

// flexMessage constructs a single LINE flex message per the official messaging
// schema: contents is ONE BubbleContainer object (NOT an array). Only carousel
// multi-flex wraps bubbles in an array.
func flexMessage(text string) map[string]interface{} {
	return map[string]interface{}{
		"type":    "flex",
		"altText": "RAG 回答",
		"contents": bubbleContainer(text),
	}
}

// bubbleContainer returns a LINE BubbleContainer with a "RAG 回答" header and a
// body whose answer renders as Markdown flex components followed by the
// WeKnora source line.
func bubbleContainer(text string) map[string]interface{} {
	return map[string]interface{}{
		"type": "bubble",
		"header": map[string]interface{}{
			"type":   "box",
			"layout": "vertical",
			"contents": []interface{}{
				map[string]interface{}{
					"type":   "text",
					"text":   "RAG 回答",
					"weight": "bold",
					"size":   "lg",
				},
			},
		},
		"body": map[string]interface{}{
			"type":    "box",
			"layout":  "vertical",
			"spacing": "md",
			"contents": append(markdownToFlexComponents(text), sourceTag()),
		},
	}
}

// sourceTag renders the trailing WeKnora attribution line on the card.
func sourceTag() map[string]interface{} {
	return map[string]interface{}{
		"type":  "text",
		"text":  "WeKnora",
		"size":  "xs",
		"color": "#888888",
	}
}

// markdownToFlexComponents renders a Markdown string into an ordered slice of
// LINE bubble-box components (headings, lists, fenced code, dividers, and
// paragraphs). The result is spliced into the card body ahead of the source
// tag so answers render as real rich text instead of one flat string.
func markdownToFlexComponents(md string) []map[string]interface{} {
	var comps []map[string]interface{}
	var code []string
	inCode := false

	flushCode := func() {
		if !inCode {
			return
		}
		inCode = false
		if len(code) > 0 {
			comps = append(comps, codeBlockComponent(code))
			code = nil
		}
	}

	for _, raw := range strings.Split(md, "\n") {
		trimmed := strings.TrimSpace(raw)
		if strings.HasPrefix(trimmed, "```") {
			if inCode {
				flushCode()
			} else {
				inCode = true
			}
			continue
		}
		if inCode {
			code = append(code, raw)
			continue
		}
		switch {
		case trimmed == "":
			// blank line: the box spacing already provides the gap.
		case isDivider(trimmed):
			comps = append(comps, separatorComponent())
		case isHeader(trimmed):
			comps = append(comps, headerComponent(trimmed))
		case isListItem(trimmed):
			comps = append(comps, listItemComponent(trimmed))
		default:
			comps = append(comps, paragraphComponent(raw))
		}
	}
	flushCode()

	return comps
}

// isHeader reports whether s opens a Markdown heading (#).
func isHeader(s string) bool { return strings.HasPrefix(s, "#") }

// headerComponent renders a Markdown heading (#/##/###) as a bold green title;
// h1 is a touch larger, h2+ stay md per the LINE size ladder.
func headerComponent(s string) map[string]interface{} {
	level := 0
	for level < len(s) && s[level] == '#' {
		level++
	}
	size := "md"
	if level == 1 {
		size = "lg"
	}
	return map[string]interface{}{
		"type":   "text",
		"text":   strings.TrimSpace(s[level:]),
		"weight": "bold",
		"size":   size,
		"color":  "#1DB446",
		"margin": "md",
	}
}

// isDivider reports whether s is a standalone horizontal rule (---).
func isDivider(s string) bool {
	if len(s) < 3 {
		return false
	}
	for _, r := range s {
		if r != '-' {
			return false
		}
	}
	return true
}

// separatorComponent renders a LINE divider between sections.
func separatorComponent() map[string]interface{} {
	return map[string]interface{}{
		"type":   "separator",
		"margin": "md",
	}
}

// isListItem reports whether s opens an unordered list item (- or *).
func isListItem(s string) bool {
	return strings.HasPrefix(s, "- ") || strings.HasPrefix(s, "* ")
}

// listItemComponent renders a list item as a bullet followed by wrapped text.
func listItemComponent(s string) map[string]interface{} {
	body := strings.TrimPrefix(strings.TrimPrefix(s, "- "), "* ")
	return map[string]interface{}{
		"type":     "box",
		"layout":   "horizontal",
		"spacing":  "sm",
		"contents": []interface{}{
			map[string]interface{}{"type": "text", "text": "•", "size": "sm", "color": "#1DB446"},
			map[string]interface{}{"type": "text", "text": body, "wrap": true},
		},
	}
}

// codeBlockComponent renders a fenced code block on a soft gray card.
func codeBlockComponent(lines []string) map[string]interface{} {
	contents := make([]interface{}, len(lines))
	for i, ln := range lines {
		contents[i] = map[string]interface{}{
			"type": "text",
			"text": ln,
			"wrap": true,
		}
	}
	return map[string]interface{}{
		"type":            "box",
		"layout":          "vertical",
		"backgroundColor": "#F4F4F5",
		"paddingAll":      "sm",
		"cornerRadius":    "sm",
		"spacing":         "sm",
		"contents":        contents,
	}
}

// paragraphComponent renders a Markdown paragraph. Inline **bold** lifts the
// whole line (LINE has no per-span weight) and `code` backticks are dropped
// (no monospace support), keeping the wire payload flat.
func paragraphComponent(raw string) map[string]interface{} {
	text := strings.ReplaceAll(strings.TrimSpace(raw), "**", "")
	text = strings.ReplaceAll(text, "`", "")
	component := map[string]interface{}{
		"type":   "text",
		"text":   text,
		"wrap":   true,
		"size":   "sm",
		"color":  "#333333",
		"margin": "xs",
	}
	if strings.Contains(raw, "**") {
		component["weight"] = "bold"
	}
	return component
}

// SendReply sends a reply back to the LINE platform. When the incoming request
// carried a reply token it is used; otherwise the call degrades to push.
func (a *Adapter) SendReply(ctx context.Context, incoming *im.IncomingMessage, reply *im.ReplyMessage) error {
	if incoming == nil {
		return fmt.Errorf("line incoming message is required")
	}

	// LINE is locked to webhook + direct (non-streaming) output. Even if a
	// streaming intermediate update is passed in, only the final answer is
	// emitted.
	if !shouldEmitDirectReply(reply) {
		return nil
	}

	text := strings.TrimSpace(im.FormatIMDisplayContent(reply.Content, im.StreamDisplayFinal))
	if text == "" {
		return nil
	}

	// Emit a structured flex card when the channel opted in; otherwise fall
	// back to a plain text message. The builder keeps the wire shape in one place.
	messages := flexMessages(text, a.enableFlexMessage)

	// Prefer the reply API. A replyToken is single-use and expires fast, so we
	// degrade to push only when the reply API genuinely fails. A successful
	// reply returns immediately — the push fallback must never also fire,
	// otherwise the user receives the same answer twice.
	if replyToken := incoming.Extra["replyToken"]; replyToken != "" {
		err := a.sendReply(ctx, replyToken, messages)
		if err == nil {
			return nil
		}
		logger.Warnf(ctx, "[IM/Line] reply API failed, falling back to push: %v", err)
	}

	// Push fallback: replyToken absent/expired, or the reply API failed above.
	// resolveTarget maps group/room -> ChatID and DM -> UserID so push reaches
	// the original conversation. This branch executes at most once.
	target := resolveTarget(incoming)
	if target == "" {
		return fmt.Errorf("line: no target user or chat id for push reply")
	}
	logger.Warnf(ctx, "[IM/Line] falling back to push to %s", target)
	return a.sendPush(ctx, target, messages)
}

// HandleURLVerification handles LINE webhook verification / health-check
// requests (requests without a populated events array).
func (a *Adapter) HandleURLVerification(c *gin.Context) bool {
	im.LimitWebhookBody(c)
	bodyBytes, err := io.ReadAll(c.Request.Body)
	if err != nil {
		return false
	}
	c.Request.Body = io.NopCloser(bytes.NewReader(bodyBytes))

	// A request without well-formed events is treated as a verification or
	// health check rather than a message event. A malformed payload is NOT a
	// verification: return false so the error is surfaced by the caller
	// instead of being swallowed as "handled".
	payload, err := parseCallback(bodyBytes)
	if err != nil {
		return false
	}
	if len(payload.Events) == 0 {
		c.JSON(http.StatusOK, gin.H{"handled": "line-webhook-verification"})
		return true
	}
	return false
}

// callAPI posts a JSON body to the LINE Messaging API.
func (a *Adapter) callAPI(ctx context.Context, url string, body interface{}) error {
	jsonBody, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal line body: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(jsonBody))
	if err != nil {
		return fmt.Errorf("create line request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+a.channelAccessToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("line api request: %w", err)
	}
	defer resp.Body.Close()

	// Read the full body up front so LINE's error payload
	// ({"message":..., "details":[...]}) is visible even on HTTP 4xx/5xx,
	// rather than being swallowed by the ok/result decode.
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read line response body: %w", err)
	}

	// A 2xx HTTP status (e.g. 200 OK with `{}`) is success.
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}

	// A non-2xx HTTP status is a definitive failure: report it with the raw
	// body so the operator sees exactly why the API rejected the request.
	return fmt.Errorf("line api %s failed: status=%d, body=%s", url, resp.StatusCode, string(respBody))
}

// httpClient is a shared HTTP client with an SSRF guard and a bounded timeout
// for LINE API calls. It mirrors the pattern used by other IM adapters.
var httpClient = secutils.NewSSRFSafeHTTPClient(secutils.SSRFSafeHTTPClientConfig{
	Timeout:      15 * time.Second,
	MaxRedirects: 3,
})

// webhookPayload is the top-level LINE webhook envelope.
type webhookPayload struct {
	Destination string          `json:"destination"`
	Events      []webhookEvent  `json:"events"`
}

// webhookEvent is a single incoming LINE event.
type webhookEvent struct {
	Type       string        `json:"type"`
	Token      string        `json:"token"`
	Timestamp  int64         `json:"timestamp"`
	Source     webhookSource `json:"source"`
	ReplyToken string        `json:"replyToken"`
	Message    *webhookMessage `json:"message"`
}

// webhookSource identifies the origin of an event.
type webhookSource struct {
	Type    string `json:"type"`
	UserID  string `json:"userId"`
	GroupID string `json:"groupId"`
	RoomID  string `json:"roomId"`
}

// webhookMessage is a message payload (text subset for this phase).
type webhookMessage struct {
	Type string `json:"type"`
	ID   string `json:"id"`
	Text string `json:"text"`
	// Mention is populated for group/room messages that contain @ mentions.
	Mention *mention `json:"mention"`
}

// mention lists the @ targets referenced by a group/room message.
type mention struct {
	Mentionees []mentionee `json:"mentionees"`
}

// mentionee is a single @ mention target. Type is "bot" when the mention is the
// bot itself; IsSelf is set by the LINE platform when the bot is a target.
type mentionee struct {
	Index  int    `json:"index"`
	Type   string `json:"type"`
	ID     string `json:"id"`
	IsSelf bool   `json:"isSelf"`
}
