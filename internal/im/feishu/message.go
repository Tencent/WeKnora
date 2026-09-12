package feishu

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/Tencent/WeKnora/internal/im"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

var _ im.MessageReader = (*Adapter)(nil)

var mentionPlaceholder = regexp.MustCompile(`@_user_[0-9]+`)

type messageMention struct{ key, id, name string }

// parseIncoming is shared by WebSocket and webhook delivery. Bot identity is
// checked against an actual mention in the current body, never a quoted body.
func (a *Adapter) parseIncoming(
	ctx context.Context, msg *feishuMessage, senderID, threadID string,
) (*im.IncomingMessage, error) {
	switch msg.MessageType {
	case "text", "post", "image", "file", "merge_forward", "interactive":
	default:
		return nil, nil
	}
	// A card is material, including its mentions and commands. Only a proven
	// user can submit one directly; groups require a separate text/post @.
	if msg.MessageType == "interactive" && (msg.SenderType != "user" || senderID == "" || msg.ChatType != "p2p") {
		return nil, nil
	}
	chatType, chatID := im.ChatTypeDirect, ""
	botID := ""
	if msg.ChatType == "group" || msg.ChatType == "topic_group" {
		chatType, chatID = im.ChatTypeGroup, msg.ChatID
		var err error
		botID, err = a.getBotOpenID(ctx)
		if err != nil {
			return nil, fmt.Errorf("resolve bot identity for mention check: %w", err)
		}
	}
	var mentions []messageMention
	for _, mention := range msg.Mentions {
		if mention != nil && mention.Id != nil {
			mentions = append(mentions, messageMention{
				ptrStr(mention.Key), ptrStr(mention.Id.OpenId), ptrStr(mention.Name),
			})
		}
	}
	material := &im.MessageMaterial{
		MessageID: msg.MessageID, ParentID: msg.ParentID,
		ResourceMessageID: msg.MessageID, ChatID: msg.ChatID,
		SenderID: senderID, SenderType: msg.SenderType, CreateTime: msg.CreateTime,
		UpdateTime: msg.UpdateTime, SnapshotSource: "event", Type: msg.MessageType,
	}
	content, mentioned, commandAllowed, err := parseMaterialBody(material, msg.Content, mentions, botID)
	if err != nil {
		return nil, err
	}
	if chatType == im.ChatTypeGroup && !mentioned {
		return nil, nil
	}
	// Match Content's outer whitespace normalization so material text cannot
	// bypass the current-input limit with whitespace discarded from the query.
	for i := range material.Parts {
		part := &material.Parts[i]
		if part.Type == "" {
			part.Text = strings.TrimLeftFunc(part.Text, unicode.IsSpace)
			if part.Text != "" {
				break
			}
		}
	}
	for i := len(material.Parts) - 1; i >= 0; i-- {
		part := &material.Parts[i]
		if part.Type == "" {
			part.Text = strings.TrimRightFunc(part.Text, unicode.IsSpace)
			if part.Text != "" {
				break
			}
		}
	}
	incoming := &im.IncomingMessage{
		Platform: a.region.Platform, MessageType: im.MessageTypeText,
		UserID: senderID, ChatID: chatID, ChatType: chatType,
		MessageID: msg.MessageID, ThreadID: threadID,
		Content: content, Material: material,
		SkipCommand: im.LooksLikeCommand(content) && !commandAllowed,
	}
	if msg.MessageType == "text" && msg.ParentID == "" && content != "" {
		incoming.Material = nil
	}
	// Keep the legacy single-file fields for existing callers. The worker uses
	// Material.Parts so every image is retained, including image-only posts.
	for _, part := range material.Parts {
		if part.Type == im.MessageTypeImage || part.Type == im.MessageTypeFile {
			incoming.MessageType = part.Type
			incoming.FileKey, incoming.FileName, incoming.FileSize = part.FileKey, part.FileName, part.FileSize
			break
		}
	}
	return incoming, nil
}

type postElement struct {
	Tag      string `json:"tag"`
	Text     string `json:"text"`
	UserID   string `json:"user_id"`
	UserName string `json:"user_name"`
	ImageKey string `json:"image_key"`
	Href     string `json:"href"`
}

type postBody struct {
	Title     string          `json:"title"`
	Content   [][]postElement `json:"content"`
	ContentV2 [][]postElement `json:"content_v2"`
}

// parseMaterialBody keeps layout separate from the current textual request.
// Resource placeholders must not turn an image-only post into a user command.
func parseMaterialBody(
	material *im.MessageMaterial, raw string, mentions []messageMention, botID string,
) (string, bool, bool, error) {
	var text, commandText strings.Builder
	mentioned := false
	appendPart := func(value string) {
		text.WriteString(value)
		if value != "" {
			material.Parts = append(material.Parts, im.MaterialPart{Text: value})
		}
	}
	appendVisibleText := func(value string) string {
		value = mentionPlaceholder.ReplaceAllStringFunc(value, func(key string) string {
			for _, mention := range mentions {
				if mention.key != key {
					continue
				}
				if botID != "" && mention.id == botID {
					mentioned = true
					return ""
				}
				if mention.name != "" {
					return "@" + mention.name
				}
				return "@" + mention.id
			}
			return key
		})
		appendPart(value)
		return value
	}
	appendText := func(value string) string {
		commandText.WriteString(value)
		return appendVisibleText(value)
	}
	switch material.Type {
	case "text":
		var body struct {
			Text string `json:"text"`
		}
		if err := json.Unmarshal([]byte(raw), &body); err != nil {
			return "", false, false, fmt.Errorf("decode text message: %w", err)
		}
		appendText(body.Text)
	case "image", "file":
		var body struct {
			ImageKey string `json:"image_key"`
			FileKey  string `json:"file_key"`
			FileName string `json:"file_name"`
			FileSize int64  `json:"file_size"`
		}
		if err := json.Unmarshal([]byte(raw), &body); err != nil {
			return "", false, false, fmt.Errorf("decode resource message: %w", err)
		}
		part := im.MaterialPart{
			Type: im.MessageType(material.Type), FileKey: body.FileKey,
			FileName: body.FileName, FileSize: body.FileSize,
		}
		if material.Type == "image" {
			part.FileKey, part.FileName = body.ImageKey, body.ImageKey+".png"
		}
		if part.FileKey == "" {
			material.Unavailable = "消息未提供资源标识"
		} else {
			material.Parts = append(material.Parts, part)
		}
	case "post":
		var body postBody
		if err := json.Unmarshal([]byte(raw), &body); err != nil {
			return "", false, false, fmt.Errorf("decode post message: %w", err)
		}
		if body.Title == "" && len(body.Content) == 0 && len(body.ContentV2) == 0 {
			// Some API responses wrap posts in locale keys. Pick one version,
			// deterministically, rather than duplicating translated images.
			var locales map[string]json.RawMessage
			if err := json.Unmarshal([]byte(raw), &locales); err != nil {
				return "", false, false, err
			}
			keys := make([]string, 0, len(locales))
			for key := range locales {
				if key != "zh_cn" && key != "en_us" {
					keys = append(keys, key)
				}
			}
			sort.Strings(keys)
			for _, key := range append([]string{"zh_cn", "en_us"}, keys...) {
				var candidate postBody
				if json.Unmarshal(locales[key], &candidate) == nil &&
					(candidate.Title != "" || len(candidate.Content) > 0 || len(candidate.ContentV2) > 0) {
					body = candidate
					break
				}
			}
		}
		var commandParts []string
		if body.Title != "" {
			commandParts = append(commandParts, body.Title)
			appendVisibleText(body.Title + "\n")
		}
		rows := body.Content
		if len(body.ContentV2) > 0 {
			rows = body.ContentV2
		}
		for _, row := range rows {
			commandText.Reset()
			for _, element := range row {
				switch element.Tag {
				case "text":
					appendText(element.Text)
				case "md", "code_block":
					appendVisibleText(element.Text)
				case "a":
					label := appendText(element.Text)
					// URLs are data, never commands or mention placeholders. Deduplicate
					// against the rendered label, which mention replacement may change.
					if element.Href != "" && element.Href != label {
						if label == "" {
							appendPart(element.Href)
						} else {
							appendPart("（" + element.Href + "）")
						}
					}
				case "at":
					id := element.UserID
					for _, mention := range mentions {
						if mention.key == id {
							id = mention.id
						}
					}
					if botID != "" && id == botID {
						mentioned = true
					} else if element.UserName != "" {
						appendPart("@" + element.UserName)
					} else {
						appendPart("@" + id)
					}
				case "img":
					material.Parts = append(material.Parts, im.MaterialPart{
						Type: im.MessageTypeImage, FileKey: element.ImageKey, FileName: element.ImageKey + ".png",
					})
				case "hr":
					appendPart("\n")
				default:
					material.Parts = append(material.Parts, im.MaterialPart{Type: im.MessageType(element.Tag)})
				}
			}
			if line := strings.TrimSpace(commandText.String()); line != "" {
				commandParts = append(commandParts, line)
			}
			appendPart("\n")
		}
		commandText.Reset()
		commandText.WriteString(strings.Join(commandParts, "\n"))
	case "merge_forward":
		// Children and their parent links come from ReadMessage, not this body.
	case "interactive":
		// Defer parsing until the worker can read the current authorized snapshot
		// under the shared preparation deadline. Never classify an event preview.
		material.RawContent = raw
	default:
		material.Unavailable = "不支持的消息正文类型：" + material.Type
	}
	content := strings.TrimSpace(text.String())
	// Preserve existing slash-command normalization: leading group mention
	// placeholders and post at-elements do not become command prefixes or args.
	// Material parts retain their text; fetched messages never dispatch commands.
	command := commandText.String()
	if botID != "" {
		for strings.HasPrefix(command, "@_user_") {
			idx := strings.IndexByte(command, ' ')
			if idx < 0 {
				break
			}
			command = command[idx+1:]
		}
	}
	command = strings.TrimSpace(command)
	commandAllowed := im.LooksLikeCommand(command)
	if commandAllowed {
		content = command
	}
	return content, mentioned, commandAllowed, nil
}

type messageAPIError struct {
	Status    int
	Code      int    `json:"code"`
	Msg       string `json:"msg"`
	Uncertain bool   `json:"-"`
}

func (e *messageAPIError) Error() string {
	return fmt.Sprintf("read Feishu API: HTTP %d code=%d %s", e.Status, e.Code, e.Msg)
}

// getAPIJSON uses the same tenant identity and region as resource downloads.
func (a *Adapter) getAPIJSON(ctx context.Context, path string, result any) error {
	token, err := a.getTenantAccessToken(ctx)
	if err != nil {
		// An identity failure cannot authorize use of an older event body.
		return &messageAPIError{Msg: "robot identity unavailable: " + err.Error()}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.api("%s", path), nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	// Detect overflow, including a valid JSON prefix followed by extra bytes.
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, (32<<20)+1))
	// Check access decisions before decoding data. A malformed items value must
	// not turn an explicit denial into a recoverable parse failure, even when
	// the response framing is truncated after a complete error envelope.
	var envelope struct {
		Code *int            `json:"code"`
		Msg  json.RawMessage `json:"msg"`
	}
	envelopeErr := json.Unmarshal(body, &envelope)
	apiErr := &messageAPIError{
		Status: resp.StatusCode, Uncertain: readErr != nil || envelopeErr != nil || envelope.Code == nil,
	}
	_ = json.Unmarshal(envelope.Msg, &apiErr.Msg)
	if envelope.Code != nil {
		apiErr.Code = *envelope.Code
	}
	if resp.StatusCode != http.StatusOK || apiErr.Code != 0 {
		return apiErr
	}
	if apiErr.Uncertain || len(body) > 32<<20 {
		apiErr.Uncertain = true
		apiErr.Msg = "API response incomplete, invalid or exceeds 32 MiB"
		return apiErr
	}
	if err := json.Unmarshal(body, result); err != nil {
		// A partially decoded DTO may already contain a revoked target. Only
		// the card body parser, after visibility checks, can permit fallback.
		return &messageAPIError{Status: resp.StatusCode, Uncertain: true, Msg: "API response data invalid"}
	}
	return nil
}

func (a *Adapter) getBotOpenID(ctx context.Context) (string, error) {
	a.botMu.Lock()
	defer a.botMu.Unlock()
	if a.botOpenID != "" {
		return a.botOpenID, nil
	}
	var result struct {
		Code int `json:"code"`
		Bot  struct {
			OpenID string `json:"open_id"`
		} `json:"bot"`
	}
	if err := a.getAPIJSON(ctx, "/open-apis/bot/v3/info", &result); err != nil {
		return "", err
	}
	if result.Code != 0 || result.Bot.OpenID == "" {
		return "", fmt.Errorf("bot identity unavailable: code=%d", result.Code)
	}
	a.botOpenID = result.Bot.OpenID
	return a.botOpenID, nil
}

// ReadMessage returns immutable snapshots; a caller must apply path depth and
// budgets on each traversal, not cache a previously truncated expansion.
func (a *Adapter) ReadMessage(
	ctx context.Context, messageID string,
) ([]*im.MessageMaterial, error) {
	return a.ReadMessageWithFallback(ctx, messageID, nil)
}

// ReadMessageWithFallback keeps Feishu event recovery optional for message readers.
func (a *Adapter) ReadMessageWithFallback(
	ctx context.Context, messageID string, event *im.MessageMaterial,
) ([]*im.MessageMaterial, error) {
	if !feishuSafePathParam(messageID) {
		return nil, fmt.Errorf("invalid message_id format")
	}
	var result struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			Items []*larkim.Message `json:"items"`
		} `json:"data"`
	}
	path := "/open-apis/im/v1/messages/" + messageID + "?user_id_type=open_id"
	read := func(withCardParameter bool) error {
		result.Code, result.Msg, result.Data.Items = 0, "", nil
		url := path
		if withCardParameter {
			url += "&card_msg_content_type=user_card_content"
		}
		if err := a.getAPIJSON(ctx, url, &result); err != nil {
			return err
		}
		if result.Code != 0 {
			return &messageAPIError{Status: http.StatusOK, Code: result.Code, Msg: result.Msg}
		}
		return nil
	}
	err := read(true)
	var apiErr *messageAPIError
	if errors.As(err, &apiErr) && apiErr.Code == 230001 &&
		strings.Contains(apiErr.Msg, "card_msg_content_type") && ctx.Err() == nil {
		// Only an explicit rejection of this parameter permits one retry.
		err = read(false)
	}
	budgets := make(map[string]*cardParseBudget)
	budgetFor := func(id string) *cardParseBudget {
		if budgets[id] == nil {
			budgets[id] = &cardParseBudget{}
		}
		return budgets[id]
	}
	eventFallback := func() *im.MessageMaterial {
		if event == nil || ctx.Err() != nil {
			return nil
		}
		if event.Type != "interactive" || event.MessageID != messageID || event.RawContent == "" ||
			(event.ResourceMessageID != "" && event.ResourceMessageID != messageID) {
			return nil
		}
		parsed := parseCard(ctx, event.RawContent, budgetFor(messageID))
		if len(parsed.Parts) == 0 || parsed.Status == "unreadable" || parsed.Status == "empty" {
			return nil
		}
		snapshot := *event
		snapshot.RawContent, snapshot.Unavailable = "", ""
		snapshot.Parts, snapshot.CardStatus = parsed.Parts, "unknown"
		snapshot.SnapshotSource = "event_fallback"
		snapshot.ReadTime = time.Now().UTC().Format(time.RFC3339Nano)
		snapshot.Warnings = append(parsed.Missing, "回读未成功，仅保留收到的事件片段，无法确认当前卡片完整性；请重新引用原卡片重试")
		return &snapshot
	}
	if err != nil {
		// Unknown API denials fail closed. Only transient failures may retain an
		// already authorized event fragment, never a revoked/invisible snapshot.
		apiErr = nil
		var transportErr *url.Error
		transient := errors.As(err, &transportErr)
		if errors.As(err, &apiErr) {
			transient = (apiErr.Status >= 500 || apiErr.Status == http.StatusTooManyRequests) &&
				apiErr.Code == 0 && !apiErr.Uncertain
		}
		if transient {
			if event := eventFallback(); event != nil {
				return []*im.MessageMaterial{event}, nil
			}
		}
		if apiErr != nil && event != nil &&
			event.MessageID == messageID && event.Type == "interactive" {
			reason := "卡片回读失败，请重新引用原卡片重试"
			switch apiErr.Code {
			case 230002, 230006, 230013, 230027, 230050, 99991663, 99991664:
				reason = "机器人无权读取此卡片或卡片已不可见；请检查消息权限后重试"
			case 230110:
				reason = "卡片消息已撤回，请重新发送材料"
			}
			if apiErr.Status == http.StatusUnauthorized || apiErr.Status == http.StatusForbidden {
				reason = "机器人未获准读取此卡片；请检查身份与消息权限后重试"
			}
			snapshot := *event
			snapshot.RawContent, snapshot.Parts, snapshot.Warnings = "", nil, nil
			snapshot.CardStatus, snapshot.Unavailable, snapshot.SnapshotSource = "unreadable", reason, "read_failed"
			snapshot.ReadTime = time.Now().UTC().Format(time.RFC3339Nano)
			return []*im.MessageMaterial{&snapshot}, nil
		}
		return nil, err
	}
	if len(result.Data.Items) == 0 {
		return nil, errors.New("message missing or no longer visible")
	}
	materials := make([]*im.MessageMaterial, 0, len(result.Data.Items))
	parsedCards := make(map[[2]string]cardParseResult)
	type cardSnapshot struct {
		body                            string
		message                         *im.MessageMaterial
		conflicting, deleted, knownCard bool
	}
	cardSnapshots := make(map[string]*cardSnapshot)
	for _, item := range result.Data.Items {
		if ctx.Err() != nil {
			break
		}
		if item == nil || ptrStr(item.MessageId) == "" {
			continue
		}
		material := &im.MessageMaterial{
			MessageID: ptrStr(item.MessageId), ParentID: ptrStr(item.ParentId),
			UpperMessageID: ptrStr(item.UpperMessageId), ResourceMessageID: messageID,
			ChatID: ptrStr(item.ChatId), CreateTime: ptrStr(item.CreateTime), Type: ptrStr(item.MsgType),
			UpdateTime: ptrStr(item.UpdateTime), ReadTime: time.Now().UTC().Format(time.RFC3339Nano),
			SnapshotSource: "message_read",
		}
		if item.Sender != nil {
			material.SenderID = ptrStr(item.Sender.Id)
			material.SenderType = ptrStr(item.Sender.SenderType)
		}
		var mentions []messageMention
		for _, mention := range item.Mentions {
			if mention != nil {
				mentions = append(mentions, messageMention{
					ptrStr(mention.Key), ptrStr(mention.Id), ptrStr(mention.Name),
				})
			}
		}
		if item.Deleted != nil && *item.Deleted {
			material.Unavailable = "消息已撤回"
		} else if item.Body == nil || item.Body.Content == nil {
			material.Unavailable = "消息正文缺失"
		} else if material.Type == "interactive" {
			// Nested forwards can repeat a snapshot. Reuse only identical bodies
			// within this response; changed bodies still share the message budget.
			key := [2]string{material.MessageID, *item.Body.Content}
			parsed, exists := parsedCards[key]
			if !exists {
				parsed = parseCard(ctx, *item.Body.Content, budgetFor(material.MessageID))
				parsedCards[key] = parsed
			}
			material.Parts, material.CardStatus, material.Warnings = parsed.Parts, parsed.Status, parsed.Missing
			if parsed.Status == "unreadable" {
				material.Unavailable = "卡片正文无法解析"
				if material.MessageID == messageID {
					if event := eventFallback(); event != nil {
						// Only the body failed. Keep the authorized message's
						// parent relation and provenance when retaining an old fragment.
						material.Parts, material.CardStatus = event.Parts, event.CardStatus
						material.Warnings, material.SnapshotSource = event.Warnings, event.SnapshotSource
						material.Unavailable = ""
					}
				}
			}
		} else if _, _, _, err := parseMaterialBody(material, *item.Body.Content, mentions, ""); err != nil {
			material.Parts = nil
			material.Unavailable = "消息正文无法解析"
		}
		if material.Type == "interactive" && material.CardStatus == "" {
			material.CardStatus = "unreadable"
		}
		deleted := item.Deleted != nil && *item.Deleted
		if material.Type == "interactive" || deleted {
			body := ""
			if item.Body != nil {
				body = ptrStr(item.Body.Content)
			}
			snapshot := cardSnapshots[material.MessageID]
			if snapshot == nil {
				snapshot = &cardSnapshot{body: body, message: material}
				cardSnapshots[material.MessageID] = snapshot
			} else {
				snapshot.conflicting = snapshot.conflicting || snapshot.body != body
				previousStatus := snapshot.message.CardStatus
				if !snapshot.deleted && (previousStatus == "unreadable" || previousStatus == "empty") &&
					material.CardStatus != "unreadable" {
					snapshot.message = material
				}
			}
			snapshot.knownCard = snapshot.knownCard || material.Type == "interactive"
			if deleted {
				snapshot.message, snapshot.deleted = material, true
			}
		}
		materials = append(materials, material)
	}
	// The material graph indexes each message ID once. Conflicting occurrences
	// must retain one usable snapshot with its own provenance; deletion wins.
	for _, snapshot := range cardSnapshots {
		if !snapshot.knownCard {
			continue
		}
		if snapshot.deleted {
			selected := *snapshot.message
			selected.Type, selected.CardStatus = "interactive", "unreadable"
			snapshot.message = &selected
		} else if snapshot.conflicting {
			selected := *snapshot.message
			if selected.CardStatus != "unreadable" && selected.CardStatus != "partial" {
				selected.CardStatus = "unknown"
			}
			selected.Warnings = append(append([]string(nil), selected.Warnings...),
				"同一卡片返回的正文不一致或缺失，无法确认完整性；未合并正文，读取情况以保留快照为准")
			snapshot.message = &selected
		}
	}
	for _, material := range materials {
		snapshot := cardSnapshots[material.MessageID]
		if snapshot != nil && snapshot.knownCard && (snapshot.conflicting || snapshot.deleted) {
			upper, parent := material.UpperMessageID, material.ParentID
			*material = *snapshot.message
			material.UpperMessageID, material.ParentID = upper, parent
		}
	}
	return materials, nil
}
