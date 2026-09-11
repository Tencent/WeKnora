package feishu

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strings"
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
	case "text", "post", "image", "file", "merge_forward":
	default:
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
		SenderID: senderID, CreateTime: msg.CreateTime, Type: msg.MessageType,
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
	appendVisibleText := func(value string) {
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
	}
	appendText := func(value string) {
		commandText.WriteString(value)
		appendVisibleText(value)
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
					if element.Text == "" {
						appendVisibleText(element.Href)
					} else {
						appendText(element.Text)
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

// getAPIJSON uses the same tenant identity and region as resource downloads.
func (a *Adapter) getAPIJSON(ctx context.Context, path string, result any) error {
	token, err := a.getTenantAccessToken(ctx)
	if err != nil {
		return err
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
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("read Feishu API: HTTP %d", resp.StatusCode)
	}
	// Bound an API response even when a forward contains more than we admit.
	return json.NewDecoder(io.LimitReader(resp.Body, 32<<20)).Decode(result)
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
func (a *Adapter) ReadMessage(ctx context.Context, messageID string) ([]*im.MessageMaterial, error) {
	if !feishuSafePathParam(messageID) {
		return nil, fmt.Errorf("invalid message_id format")
	}
	var result struct {
		Code int `json:"code"`
		Data struct {
			Items []*larkim.Message `json:"items"`
		} `json:"data"`
	}
	if err := a.getAPIJSON(ctx, "/open-apis/im/v1/messages/"+messageID+"?user_id_type=open_id", &result); err != nil {
		return nil, err
	}
	if result.Code != 0 || len(result.Data.Items) == 0 {
		return nil, fmt.Errorf("message unavailable: code=%d", result.Code)
	}
	materials := make([]*im.MessageMaterial, 0, len(result.Data.Items))
	for _, item := range result.Data.Items {
		if item == nil || ptrStr(item.MessageId) == "" {
			continue
		}
		material := &im.MessageMaterial{
			MessageID: ptrStr(item.MessageId), ParentID: ptrStr(item.ParentId),
			UpperMessageID: ptrStr(item.UpperMessageId), ResourceMessageID: messageID,
			ChatID: ptrStr(item.ChatId), CreateTime: ptrStr(item.CreateTime), Type: ptrStr(item.MsgType),
		}
		if item.Sender != nil {
			material.SenderID = ptrStr(item.Sender.Id)
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
		} else if _, _, _, err := parseMaterialBody(material, *item.Body.Content, mentions, ""); err != nil {
			material.Parts = nil
			material.Unavailable = "消息正文无法解析"
		}
		materials = append(materials, material)
	}
	return materials, nil
}
