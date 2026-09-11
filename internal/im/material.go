package im

import (
	"context"
	"errors"
	"fmt"
	"html"
	"strings"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/google/uuid"
)

const (
	maxIMMaterialDepth       = 10
	maxIMMaterialMessages    = 50
	maxIMMaterialAttachments = 10
)

var errIMDownloadBudget = errors.New("本轮剩余下载额度已用尽，未取得可确认完整的资源")

type imMaterialBudget struct {
	downloadRemaining int64
	textRemaining     int
}

// A message ID alone cannot identify a forwarded snapshot's resource keys.
type (
	materialKey         struct{ context, message string }
	materialResourceKey struct{ context, key string }
)

type materialRead struct {
	messages map[string]*MessageMaterial
	children map[string][]*MessageMaterial
	err      error
}

type imPreparedMaterials struct {
	attachments types.MessageAttachments
	images      []string
	directFiles []*imDownloadedAttachment
	context     strings.Builder
	warnings    []string
	textCount   int
}

type materialPreparation struct {
	ctx       context.Context
	service   *Service
	adapter   Adapter
	reader    MessageReader
	budget    imMaterialBudget
	result    imPreparedMaterials
	reads     map[string]*materialRead
	admitted  map[materialKey]bool
	resources map[materialResourceKey]int // attachment index; -1 for a failed attempt
	path      map[string]bool
	noted     map[string]bool
}

func newMaterialPreparation(ctx context.Context, service *Service, adapter Adapter) *materialPreparation {
	reader, _ := adapter.(MessageReader)
	return &materialPreparation{
		ctx: ctx, service: service, adapter: adapter, reader: reader,
		budget: imMaterialBudget{downloadRemaining: maxIMAttachmentBytes, textRemaining: maxIMAttachmentContentBytes},
		reads:  make(map[string]*materialRead), admitted: make(map[materialKey]bool),
		resources: make(map[materialResourceKey]int), path: make(map[string]bool), noted: make(map[string]bool),
	}
}

func (s *Service) prepareIMMaterials(ctx context.Context, msg *IncomingMessage, adapter Adapter) imPreparedMaterials {
	prepareCtx, cancel := context.WithTimeout(ctx, imAttachmentReadTimeout)
	defer cancel()
	p := newMaterialPreparation(prepareCtx, s, adapter)
	root := msg.Material
	if root.Type != "merge_forward" {
		p.reads[root.MessageID] = &materialRead{messages: map[string]*MessageMaterial{root.MessageID: root}}
	}
	p.visit(root, 0, true)
	if prepareCtx.Err() != nil {
		p.note("材料准备已停止：超时或请求被取消，后续材料未纳入本次分析。")
	}
	if root.Type == "post" && root.ParentID == "" && len(p.resources) == 0 && len(p.result.warnings) == 0 {
		// Current text alone is not a supplement that can override a KB's fixed fallback.
		p.result.context.Reset()
	}
	return p.result
}

func (p *materialPreparation) note(message string) {
	if !p.noted[message] {
		p.noted[message] = true
		p.result.warnings = append(p.result.warnings, message)
	}
}

func (p *materialPreparation) text(value string) string {
	limited := truncateUTF8ByBytes(value, p.budget.textRemaining)
	p.budget.textRemaining -= len(limited)
	if len(limited) < len(value) {
		p.note("材料文本额度已用尽，后续正文或解析文本未完整纳入本次分析。")
	}
	return limited
}

func materialIdentity(material *MessageMaterial) materialKey {
	contextID := material.ResourceMessageID
	if contextID == "" {
		contextID = material.MessageID
	}
	return materialKey{contextID, material.MessageID}
}

func (p *materialPreparation) read(messageID string) *materialRead {
	if cached, ok := p.reads[messageID]; ok {
		return cached
	}
	read := &materialRead{messages: make(map[string]*MessageMaterial), children: make(map[string][]*MessageMaterial)}
	p.reads[messageID] = read
	if p.reader == nil {
		read.err = errors.New("adapter does not support reading messages")
		return read
	}
	items, err := p.reader.ReadMessage(p.ctx, messageID)
	if err != nil {
		read.err = err
		return read
	}
	for _, item := range items {
		if item == nil || item.MessageID == "" {
			continue
		}
		snapshot := *item
		if snapshot.ResourceMessageID == "" {
			snapshot.ResourceMessageID = messageID
		}
		read.messages[snapshot.MessageID] = &snapshot
		if snapshot.UpperMessageID != "" {
			read.children[snapshot.UpperMessageID] = append(read.children[snapshot.UpperMessageID], &snapshot)
		}
	}
	if read.messages[messageID] == nil {
		read.err = errors.New("requested message missing from API response")
	}
	return read
}

func (p *materialPreparation) canVisit(key materialKey, depth int) bool {
	if p.ctx.Err() != nil {
		return false
	}
	if depth > maxIMMaterialDepth {
		p.note(fmt.Sprintf("消息 %s 超过 10 层展开上限，未纳入本次分析。", key.message))
		return false
	}
	if p.path[key.message] {
		p.note(fmt.Sprintf("消息 %s 的引用或转发路径形成循环，该分支已停止。", key.message))
		return false
	}
	if !p.admitted[key] && len(p.admitted) >= maxIMMaterialMessages {
		p.note("消息数量额度已用尽，后续新消息未纳入本次分析。")
		return false
	}
	return true
}

func (p *materialPreparation) visit(material *MessageMaterial, depth int, current bool) {
	key := materialIdentity(material)
	if !p.canVisit(key, depth) {
		return
	}
	p.path[key.message] = true
	defer delete(p.path, key.message)
	first := !p.admitted[key]
	p.admitted[key] = true

	var read *materialRead
	if material.Type == "merge_forward" {
		read = p.read(key.context)
		if read.err != nil {
			logger.Warnf(p.ctx, "[IM] forward read failed: message=%s error=%v", material.MessageID, read.err)
			p.note(fmt.Sprintf("转发消息 %s 读取失败，其子消息未纳入本次分析。", material.MessageID))
		} else if snapshot := read.messages[material.MessageID]; snapshot != nil {
			resolved := *snapshot
			// The current event may carry a reference/source omitted by the
			// fetched forward container. Do not discard already-known metadata.
			if current {
				if material.ParentID != "" {
					resolved.ParentID = material.ParentID
				}
				if material.ChatID != "" {
					resolved.ChatID = material.ChatID
				}
				if material.SenderID != "" {
					resolved.SenderID = material.SenderID
				}
				if material.CreateTime != "" {
					resolved.CreateTime = material.CreateTime
				}
			}
			material = &resolved
		}
	}

	if first {
		fmt.Fprintf(&p.result.context,
			"\n<message id=\"%s\" chat=\"%s\" resource_context=\"%s\" depth=\"%d\" "+
				"sender=\"%s\" time=\"%s\" type=\"%s\">\n",
			html.EscapeString(material.MessageID), html.EscapeString(material.ChatID),
			html.EscapeString(key.context), depth,
			html.EscapeString(material.SenderID), html.EscapeString(material.CreateTime),
			html.EscapeString(material.Type))
		if material.Unavailable != "" {
			p.note(fmt.Sprintf("消息 %s：%s。", material.MessageID, material.Unavailable))
		}
		hasText := false
		for _, part := range material.Parts {
			if p.ctx.Err() != nil {
				break
			}
			if part.Type == "" {
				text := part.Text
				if !current {
					text = p.text(text)
				}
				if strings.TrimSpace(text) != "" {
					hasText = true
				}
				p.result.context.WriteString(html.EscapeString(text))
				continue
			}
			if part.Type != MessageTypeImage && part.Type != MessageTypeFile {
				p.note(fmt.Sprintf("消息 %s 包含不支持的内容类型 %s，该部分未纳入本次分析。", material.MessageID, part.Type))
				continue
			}
			p.resource(material, part, current)
		}
		if hasText {
			p.result.textCount++
		}
		p.result.context.WriteString("\n</message>\n")
	} else {
		// Reuse content, but still traverse this path: a shallower occurrence may
		// reach parents that were beyond the depth limit on the first occurrence.
		reference := fmt.Sprintf("\n[再次引用消息 %s，读取上下文 %s，深度 %d]\n", material.MessageID, key.context, depth)
		p.result.context.WriteString(html.EscapeString(p.text(reference)))
	}

	if material.ParentID != "" {
		parentKey := materialKey{material.ParentID, material.ParentID}
		var parent *MessageMaterial
		if known := p.reads[key.context]; known != nil {
			parent = known.messages[material.ParentID]
			if parent != nil {
				parentKey = materialIdentity(parent)
			}
		}
		if p.canVisit(parentKey, depth+1) {
			if parent == nil {
				parentRead := p.read(material.ParentID)
				if parentRead.err != nil {
					logger.Warnf(p.ctx, "[IM] parent read failed: message=%s error=%v",
						material.ParentID, parentRead.err)
					p.note(fmt.Sprintf("引用消息 %s 读取失败，该分支未纳入本次分析。", material.ParentID))
				} else {
					parent = parentRead.messages[material.ParentID]
				}
			}
			if parent != nil {
				p.visit(parent, depth+1, false)
			}
		}
	}
	if read != nil && read.err == nil {
		for _, child := range read.children[material.MessageID] {
			if p.ctx.Err() != nil {
				break
			}
			p.visit(child, depth+1, false)
		}
	}
}

func (p *materialPreparation) resource(material *MessageMaterial, part MaterialPart, current bool) {
	contextID := materialIdentity(material).context
	key := materialResourceKey{contextID, part.FileKey}
	if index, exists := p.resources[key]; exists {
		if index >= 0 {
			fmt.Fprintf(&p.result.context, "[附件 %d]", index+1)
		}
		return
	}
	if len(p.resources) >= maxIMMaterialAttachments {
		p.note("附件数量额度已用尽，后续新附件未纳入本次分析。")
		return
	}
	p.resources[key] = -1
	if part.FileKey == "" {
		p.note(fmt.Sprintf("消息 %s 的附件缺少资源标识，内容不可读取。", material.MessageID))
		return
	}
	fileMsg := &IncomingMessage{
		Platform: p.adapter.Platform(), MessageType: part.Type, MessageID: contextID,
		FileKey: part.FileKey, FileName: part.FileName, FileSize: part.FileSize,
	}
	attachments, images, downloaded, err := p.service.prepareIMAttachments(p.ctx, fileMsg, p.adapter, &p.budget)
	if err != nil {
		logger.Warnf(p.ctx, "[IM] material attachment failed: message=%s resource=%s error=%v",
			material.MessageID, part.FileKey, err)
		reason := "附件读取失败"
		if errors.Is(err, errIMDownloadBudget) {
			reason = errIMDownloadBudget.Error()
		}
		p.note(fmt.Sprintf("消息 %s 的附件 %s：%s。", material.MessageID, part.FileName, reason))
		return
	}
	if len(attachments) == 0 {
		return
	}
	attachment := attachments[0]
	if attachment.ContentMode == "download_prefix" {
		p.note(fmt.Sprintf("消息 %s 的附件 %s 已达到下载额度，完整性未确认；仅保留已取得的文字，不自动入库。",
			material.MessageID, attachment.FileName))
	}
	attachment.SourceMessageID = material.MessageID
	attachment.SourceChatID = material.ChatID
	attachment.ResourceMessageID = contextID
	limited := p.text(attachment.Content)
	attachment.IsTruncated = attachment.IsTruncated || len(limited) < len(attachment.Content)
	attachment.Content = limited
	if len(images) > 0 {
		attachment.ImageIndex = len(p.result.images) + 1
		p.result.images = append(p.result.images, images...)
	}
	if attachment.Content == "" && len(images) == 0 {
		p.note(fmt.Sprintf("消息 %s 的附件 %s 仅取得元数据，内容不可用。", material.MessageID, attachment.FileName))
	} else {
		if attachment.IsImage && len(images) == 0 {
			p.note(fmt.Sprintf("消息 %s 的图片 %s 仅有解析文字，不能据此判断颜色、布局或画面。", material.MessageID, attachment.FileName))
		}
	}
	if attachment.IsTruncated {
		p.note(fmt.Sprintf("消息 %s 的附件 %s 解析文本已截断。", material.MessageID, attachment.FileName))
	}
	p.resources[key] = len(p.result.attachments)
	p.result.attachments = append(p.result.attachments, attachment)
	fmt.Fprintf(&p.result.context, "[附件 %d]", len(p.result.attachments))
	if current && downloaded != nil {
		p.result.directFiles = append(p.result.directFiles, downloaded)
	}
}

func (prepared *imPreparedMaterials) quote() *QuotedMessage {
	return &QuotedMessage{MaterialContext: prepared.context.String(), MaterialWarnings: prepared.warnings}
}

func (prepared *imPreparedMaterials) receipt() string {
	images, files := 0, 0
	for _, attachment := range prepared.attachments {
		if attachment.IsImage && (attachment.ImageIndex > 0 || attachment.Content != "") {
			images++
		} else if !attachment.IsImage && attachment.Content != "" {
			files++
		}
	}
	return fmt.Sprintf("已接收可读取的材料：%d 条文字、%d 张图片、%d 个文件。尚未进行内容分析；请引用原始材料消息补充问题。",
		prepared.textCount, images, files)
}

func (prepared *imPreparedMaterials) disableImages() {
	prepared.images = nil
	for i := range prepared.attachments {
		attachment := &prepared.attachments[i]
		if attachment.ImageIndex == 0 {
			continue
		}
		attachment.ImageIndex = 0
		prepared.warnings = append(prepared.warnings, fmt.Sprintf(
			"消息 %s 的图片 %s 没有可用的原图读取模型；仅保留实际解析文字，不能据此判断颜色、布局或画面。",
			attachment.SourceMessageID, attachment.FileName))
	}
}

func appendIMMaterialWarnings(answer string, quote *QuotedMessage) string {
	if quote == nil || len(quote.MaterialWarnings) == 0 {
		return answer
	}
	return strings.TrimSpace(answer) + "\n\n材料读取说明：\n- " + strings.Join(quote.MaterialWarnings, "\n- ")
}

func imStoredUserContent(content string, quote *QuotedMessage) string {
	if quote != nil && (quote.MaterialContext != "" || len(quote.MaterialWarnings) > 0) {
		return content + "\n\n" + formatQuotedContext(quote)
	}
	return content
}

func (s *Service) recordIMReceipt(
	ctx context.Context, req *qaRequest, attachments types.MessageAttachments, answer string,
) error {
	requestID := uuid.New().String()
	if _, err := s.messageService.CreateMessage(ctx, createIMUserMessagePayload(
		req.session.ID, imStoredUserContent(req.msg.Content, req.msg.Quote), requestID, attachments,
	)); err != nil {
		return err
	}
	message := createIMAssistantMessagePayload(req.session.ID, requestID)
	message.Content, message.IsCompleted = answer, true
	_, err := s.messageService.CreateMessage(ctx, message)
	return err
}
