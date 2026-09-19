package handler

import (
	"fmt"
	"html"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// MasteryHandler exposes the knowledge-guidance endpoints: recording a page
// view, liking an answer, and clearing one's own learning profile. Every route
// operates on the caller's own scope (derived from context), matching the
// memory endpoints. Unlike the raw ledgers, the write handlers verify that the
// claimed event maps onto a real object the caller can see (a wiki page they
// viewed, a message they own) so the "auditable behavior evidence" premise
// cannot be gamed by fabricated client payloads.
type MasteryHandler struct {
	masteryService       interfaces.MasteryService
	messageService       interfaces.MessageService
	wikiService          interfaces.WikiPageService
	knowledgeBaseService interfaces.KnowledgeBaseService
}

// NewMasteryHandler creates the knowledge-guidance handler.
func NewMasteryHandler(
	masteryService interfaces.MasteryService,
	messageService interfaces.MessageService,
	wikiService interfaces.WikiPageService,
	knowledgeBaseService interfaces.KnowledgeBaseService,
) *MasteryHandler {
	return &MasteryHandler{
		masteryService:       masteryService,
		messageService:       messageService,
		wikiService:          wikiService,
		knowledgeBaseService: knowledgeBaseService,
	}
}

type recordPageViewRequest struct {
	KnowledgeBaseID string `json:"knowledge_base_id"`
	Slug            string `json:"slug"`
	// Duration is the effective viewing time in seconds (front-end cleaned).
	Duration int64 `json:"duration"`
}

// RecordPageView godoc
// @Summary      记录一次有效页面浏览
// @Tags         知识引导
// @Accept       json
// @Produce      json
// @Param        request  body  recordPageViewRequest  true  "浏览记录"
// @Success      200  {object}  map[string]interface{}
// @Router       /memory/page-view [post]
func (h *MasteryHandler) RecordPageView(c *gin.Context) {
	var req recordPageViewRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}
	if req.KnowledgeBaseID == "" || req.Slug == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "knowledge_base_id and slug are required"})
		return
	}
	if req.Duration <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "duration must be positive"})
		return
	}
	// 钳制单次时长，防止客户端提交任意秒数刷高水位。
	if req.Duration > types.MasteryMaxPageViewSeconds {
		req.Duration = types.MasteryMaxPageViewSeconds
	}
	var sourceKnowledgeIDs []string
	var neighbors []string
	if h.wikiService != nil {
		page, err := h.wikiService.GetPageBySlug(c.Request.Context(), req.KnowledgeBaseID, req.Slug)
		if err != nil || page == nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "page not found in knowledge base"})
			return
		}
		sourceKnowledgeIDs = page.SourceKnowledgeIDs()
		// 一跳邻居（出链 ∪ 入链）：读一个页面会稍微带动与它相邻的页面。
		// 链接关系只存在于 Wiki 页面，因此在这里取好再传入——mastery 服务
		// 本身不依赖图谱，避免两个服务互相依赖。
		neighbors = append(neighbors, page.OutLinks...)
		neighbors = append(neighbors, page.InLinks...)
	}
	h.masteryService.RecordPageView(c.Request.Context(), req.KnowledgeBaseID, req.Slug, req.Duration, neighbors)
	resp := gin.H{"success": true}
	// 回传该节点的最新状态，前端就地更新水位球，无需重拉整张图。
	if st, err := h.masteryService.NodeStateForSlug(c.Request.Context(), req.KnowledgeBaseID, req.Slug, sourceKnowledgeIDs); err == nil {
		resp["mastery"] = st.Level
		resp["recently_active"] = st.RecentlyActive
		resp["last_active"] = st.LastActive
	}
	c.JSON(http.StatusOK, resp)
}

type recordAnswerLikeRequest struct {
	SessionID string `json:"session_id"`
	MessageID string `json:"message_id"`
}

// RecordAnswerLike godoc
// @Summary      点赞一条 AI 回答
// @Tags         知识引导
// @Accept       json
// @Produce      json
// @Param        request  body  recordAnswerLikeRequest  true  "点赞"
// @Success      200  {object}  map[string]interface{}
// @Router       /memory/answer-like [post]
func (h *MasteryHandler) RecordAnswerLike(c *gin.Context) {
	var req recordAnswerLikeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}
	if req.SessionID == "" || req.MessageID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "session_id and message_id are required"})
		return
	}
	if h.messageService == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "message service unavailable"})
		return
	}
	// 通过服务端加载消息（GetMessage 会校验 session 归属），并从消息的真实
	// 引用中提取 knowledge_ids，而不是信任客户端提交的文档列表。
	msg, err := h.messageService.GetMessage(c.Request.Context(), req.SessionID, req.MessageID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "message not found"})
		return
	}
	refs := extractReferenceSources(msg.KnowledgeReferences)
	if len(refs) == 0 {
		c.JSON(http.StatusOK, gin.H{"success": true, "skipped": true})
		return
	}
	h.masteryService.RecordAnswerLike(c.Request.Context(), req.MessageID, refs)
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// extractReferenceSources returns the distinct, non-empty source docs a message
// actually cited, in citation order, carrying each doc's knowledge_base_id so
// the like allocation and daily bucket can be scoped to the right KB.
func extractReferenceSources(refs types.References) []types.MemoryDocAffinity {
	seen := make(map[string]struct{}, len(refs))
	out := make([]types.MemoryDocAffinity, 0, len(refs))
	for _, r := range refs {
		if r == nil || r.KnowledgeID == "" {
			continue
		}
		if _, dup := seen[r.KnowledgeID]; dup {
			continue
		}
		seen[r.KnowledgeID] = struct{}{}
		out = append(out, types.MemoryDocAffinity{
			KnowledgeID:     r.KnowledgeID,
			KnowledgeBaseID: r.KnowledgeBaseID,
			Title:           r.KnowledgeTitle,
		})
	}
	return out
}

// CancelAnswerLike godoc
// @Summary      取消点赞
// @Tags         知识引导
// @Produce      json
// @Param        message_id  path  string  true  "回答消息 ID"
// @Success      200  {object}  map[string]interface{}
// @Router       /memory/answer-like/{message_id} [delete]
func (h *MasteryHandler) CancelAnswerLike(c *gin.Context) {
	h.masteryService.CancelAnswerLike(c.Request.Context(), c.Param("message_id"))
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// DeleteMastery godoc
// @Summary      删除个人知识画像
// @Tags         知识引导
// @Produce      json
// @Success      200  {object}  map[string]interface{}
// @Router       /memory/mastery [delete]
func (h *MasteryHandler) DeleteMastery(c *gin.Context) {
	if err := h.masteryService.DeleteAll(c.Request.Context()); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

type masteryProfileRequest struct {
	KnowledgeBaseID string `json:"knowledge_base_id"`
}

// Profile godoc
// @Summary      查看个人知识画像（每个节点的证据明细）
// @Tags         知识引导
// @Produce      json
// @Param        kb_id  query  string  true  "知识库 ID"
// @Success      200  {object}  map[string]interface{}
// @Router       /memory/mastery [get]
func (h *MasteryHandler) Profile(c *gin.Context) {
	kbID := c.Query("kb_id")
	if kbID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "kb_id is required"})
		return
	}
	slugMeta, err := h.buildSlugMeta(c, kbID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	profile, err := h.masteryService.Profile(c.Request.Context(), kbID, slugMetaToSources(slugMeta))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	h.fillProfileMeta(profile, slugMeta)
	c.JSON(http.StatusOK, gin.H{"data": profile, "total": len(profile)})
}

// ExportProfile godoc
// @Summary      导出个人知识画像（HTML）
// @Tags         知识引导
// @Produce      text/html
// @Param        kb_id  query  string  true  "知识库 ID"
// @Success      200  {string}  string  "HTML"
// @Router       /memory/mastery/export [get]
func (h *MasteryHandler) ExportProfile(c *gin.Context) {
	kbID := c.Query("kb_id")
	if kbID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "kb_id is required"})
		return
	}
	slugMeta, err := h.buildSlugMeta(c, kbID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	profile, err := h.masteryService.Profile(c.Request.Context(), kbID, slugMetaToSources(slugMeta))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	h.fillProfileMeta(profile, slugMeta)

	html := h.renderProfileHTML(h.knowledgeBaseName(c, kbID), profile)
	c.Header("Content-Type", "text/html; charset=utf-8")
	c.Header("Content-Disposition", `attachment; filename="mastery-profile.html"`)
	c.String(http.StatusOK, html)
}

// knowledgeBaseName resolves the KB display name for the exported report,
// falling back to the raw id when the KB cannot be read (deleted, no
// permission), so the report never renders an empty label.
func (h *MasteryHandler) knowledgeBaseName(c *gin.Context, kbID string) string {
	if h.knowledgeBaseService == nil {
		return kbID
	}
	kb, err := h.knowledgeBaseService.GetKnowledgeBaseByID(c.Request.Context(), kbID)
	if err != nil || kb == nil || kb.Name == "" {
		return kbID
	}
	return kb.Name
}

type exposureClickRequest struct {
	KnowledgeBaseID string `json:"knowledge_base_id"`
	CandidateSlug   string `json:"candidate_slug"`
}

// ExposureClick godoc
// @Summary      记录一次边界候选点击
// @Tags         知识引导
// @Accept       json
// @Produce      json
// @Param        request  body  exposureClickRequest  true  "点击"
// @Success      200  {object}  map[string]interface{}
// @Router       /memory/exposure/click [post]
func (h *MasteryHandler) ExposureClick(c *gin.Context) {
	var req exposureClickRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}
	if req.KnowledgeBaseID == "" || req.CandidateSlug == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "knowledge_base_id and candidate_slug are required"})
		return
	}
	h.masteryService.MarkExposureClicked(c.Request.Context(), req.KnowledgeBaseID, req.CandidateSlug)
	c.JSON(http.StatusOK, gin.H{"success": true})
}

type recordExposureRequest struct {
	KnowledgeBaseID string   `json:"knowledge_base_id"`
	TriggerSlug     string   `json:"trigger_slug"`
	CandidateSlugs  []string `json:"candidate_slugs"`
}

// RecordExposure godoc
// @Summary      记录一次边界候选曝光（点击中心节点后展示的局部涟漪候选）
// @Tags         知识引导
// @Accept       json
// @Produce      json
// @Param        request  body  recordExposureRequest  true  "曝光"
// @Success      200  {object}  map[string]interface{}
// @Router       /memory/exposure [post]
func (h *MasteryHandler) RecordExposure(c *gin.Context) {
	var req recordExposureRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}
	if req.KnowledgeBaseID == "" || req.TriggerSlug == "" || len(req.CandidateSlugs) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "knowledge_base_id, trigger_slug and candidate_slugs are required"})
		return
	}
	h.masteryService.RecordExposures(c.Request.Context(), req.KnowledgeBaseID, req.TriggerSlug, req.CandidateSlugs)
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// slugMeta is the per-page projection the export/profile handlers need:
// source knowledge ids (for evidence projection) plus title and page type
// (for grouping and display).
type slugMeta struct {
	Title     string
	PageType  string
	SourceIDs []string
}

// buildSlugMeta streams all pages of a KB and maps slug → its metadata, so the
// profile endpoint can project citation/like evidence onto pages AND the export
// can group nodes by page type with human-readable titles.
func (h *MasteryHandler) buildSlugMeta(c *gin.Context, kbID string) (map[string]slugMeta, error) {
	out := make(map[string]slugMeta)
	if h.wikiService == nil {
		return out, nil
	}
	cursor := ""
	for {
		pages, next, err := h.wikiService.ListPagesCursor(c.Request.Context(), kbID, cursor, 500)
		if err != nil {
			return nil, err
		}
		for _, p := range pages {
			if p == nil {
				continue
			}
			out[p.Slug] = slugMeta{
				Title:     p.Title,
				PageType:  p.PageType,
				SourceIDs: p.SourceKnowledgeIDs(),
			}
		}
		if next == "" || len(pages) == 0 {
			break
		}
		cursor = next
	}
	return out, nil
}

// slugMetaToSources reduces slugMeta to the slug → source-knowledge-ids map the
// mastery service expects.
func slugMetaToSources(meta map[string]slugMeta) map[string][]string {
	out := make(map[string][]string, len(meta))
	for slug, m := range meta {
		out[slug] = m.SourceIDs
	}
	return out
}

// fillProfileMeta back-fills title/page-type onto each profile row from the
// page metadata, so callers don't need a second lookup.
func (h *MasteryHandler) fillProfileMeta(profile []types.MasteryNodeDetail, meta map[string]slugMeta) {
	for i := range profile {
		if m, ok := meta[profile[i].Slug]; ok {
			profile[i].Title = m.Title
			profile[i].PageType = m.PageType
		}
	}
}

// mastery tier → 中文标签（导出报告展示用）。
var masteryTierLabelZH = map[string]string{
	"none":     "不了解",
	"touch":    "接触",
	"familiar": "熟悉",
	"mastered": "掌握",
}

// tierOrder is the display order of the four tiers in the export.
var tierOrder = []string{"mastered", "familiar", "touch", "none"}

// pageTypeOrder is the display order of page types in the export.
var pageTypeOrder = []string{
	types.WikiPageTypeSummary,
	types.WikiPageTypeEntity,
	types.WikiPageTypeConcept,
	types.WikiPageTypeSynthesis,
	types.WikiPageTypeComparison,
	types.WikiPageTypeIndex,
}

// pageTypeLabelZH maps a page type to its Chinese label for the export header.
var pageTypeLabelZH = map[string]string{
	types.WikiPageTypeSummary:    "摘要",
	types.WikiPageTypeEntity:     "实体",
	types.WikiPageTypeConcept:    "概念",
	types.WikiPageTypeSynthesis:  "综合",
	types.WikiPageTypeComparison: "对比",
	types.WikiPageTypeIndex:      "索引",
}

// renderProfileHTML renders the mastery profile as a self-contained HTML page:
// one collapsible bucket per page type, with four tier sub-buckets inside each.
func (h *MasteryHandler) renderProfileHTML(kbName string, profile []types.MasteryNodeDetail) string {
	// Group by page type, then by tier.
	byType := make(map[string]map[string][]types.MasteryNodeDetail)
	for _, n := range profile {
		pt := n.PageType
		if pt == "" {
			pt = types.WikiPageTypeSummary
		}
		if byType[pt] == nil {
			byType[pt] = make(map[string][]types.MasteryNodeDetail)
		}
		byType[pt][n.Tier] = append(byType[pt][n.Tier], n)
	}

	// Summary counts per tier.
	var noneC, touchC, familiarC, masteredC int
	for _, n := range profile {
		switch n.Tier {
		case "mastered":
			masteredC++
		case "familiar":
			familiarC++
		case "touch":
			touchC++
		default:
			noneC++
		}
	}

	total := len(profile)
	pctOf := func(n int) string {
		if total == 0 {
			return "0"
		}
		return strconv.FormatFloat(float64(n)*100/float64(total), 'f', 1, 64)
	}

	var b strings.Builder
	b.WriteString("<!DOCTYPE html>\n<html lang=\"zh-CN\">\n<head>\n<meta charset=\"utf-8\">\n")
	b.WriteString("<meta name=\"viewport\" content=\"width=device-width, initial-scale=1\">\n")
	b.WriteString("<title>知识掌握画像</title>\n")
	b.WriteString(`<style>
:root {
  --gold-top: #f5d978; --gold-bot: #c98b18;
  --blue-top: #8dcae0; --blue-bot: #3c93b6;
  --white-top: #ffffff; --white-bot: #dce8f2;
  --ink: #14181e; --muted: #59636f; --line: #e4e9ef;
  font-family: -apple-system, "SF Pro Text", "Microsoft YaHei", "PingFang SC", sans-serif;
  color: var(--ink);
}
* { box-sizing: border-box; }
body {
  max-width: 920px; margin: 0 auto; padding: 48px 24px 72px;
  background: radial-gradient(1200px 600px at 50% -200px, #eef3f9 0%, #f7f8fa 60%);
  line-height: 1.5;
}
.header { margin-bottom: 28px; }
h1 { font-size: 27px; margin: 0 0 6px; letter-spacing: -0.4px; font-weight: 650; }
h1 .dot { color: #d9a93f; }
.subtitle { color: var(--muted); font-size: 13px; }
.subtitle b { color: #37414d; font-weight: 600; }

/* 概览：四档数字 + 横向分布条 */
.overview {
  background: #fff; border: 1px solid var(--line); border-radius: 16px;
  padding: 20px 22px; margin-bottom: 26px;
  box-shadow: 0 1px 2px rgba(16,24,40,.04), 0 8px 24px -12px rgba(16,24,40,.10);
}
.stats { display: flex; gap: 8px; margin-bottom: 16px; }
.stat { flex: 1; text-align: center; padding: 6px 0; border-radius: 10px; transition: background .15s; }
.stat:hover { background: #f7f9fb; }
.stat .num { font-size: 28px; font-weight: 650; font-variant-numeric: tabular-nums; line-height: 1.15; }
.stat .lbl { font-size: 12.5px; color: #4c5663; margin-top: 3px; }
.stat .pct { font-size: 11px; color: #78828f; margin-top: 1px; font-variant-numeric: tabular-nums; }
.stat.mastered .num { color: #a8700d; }
.stat.familiar .num { color: #1f6b8a; }
.stat.touch .num { color: #4f6b85; }
.stat.none .num { color: #59636f; }
/* 分布条：宽度占比 + 档位色，像一条真实的"水位谱" */
.distribution { display: flex; height: 12px; border-radius: 999px; overflow: hidden; background: #f0f3f7; }
.seg { display: block; height: 100%; transition: opacity .15s; }
.distribution .seg:hover { opacity: .82; }
.seg.mastered { background: linear-gradient(180deg, var(--gold-top), var(--gold-bot)); }
.seg.familiar { background: linear-gradient(180deg, var(--blue-top), var(--blue-bot)); }
.seg.touch { background: linear-gradient(180deg, var(--white-top), var(--white-bot)); }
.seg.none { background: #d5dce4; }
.distribution-legend { display: flex; gap: 16px; margin-top: 10px; font-size: 12px; color: var(--muted); flex-wrap: wrap; }
.distribution-legend i { display: inline-block; width: 9px; height: 9px; border-radius: 3px; margin-right: 5px; vertical-align: -1px; }

/* 页面类型桶 */
details.type {
  background: #fff; border: 1px solid var(--line); border-radius: 14px;
  margin-bottom: 14px; overflow: hidden;
  box-shadow: 0 1px 2px rgba(16,24,40,.03);
}
details.type > summary {
  cursor: pointer; padding: 15px 20px; font-size: 15px; font-weight: 620;
  list-style: none; display: flex; align-items: center; gap: 8px;
}
details.type > summary::-webkit-details-marker { display: none; }
details.type > summary::before {
  content: ""; width: 6px; height: 6px; border-right: 1.6px solid #b8c0cb; border-bottom: 1.6px solid #b8c0cb;
  transform: rotate(-45deg); transition: transform .18s; flex: none; margin-left: 2px;
}
details.type[open] > summary::before { transform: rotate(45deg); }
details.type > summary:hover { background: #fbfcfe; }
details.type > summary .count { color: #6b7683; font-weight: 500; font-size: 13px; }
details.type > summary .mini { margin-left: auto; display: flex; height: 6px; width: 84px; border-radius: 999px; overflow: hidden; background: #f0f3f7; flex: none; }
.type-body { padding: 2px 16px 16px; border-top: 1px solid #f4f6f9; }

/* 档位子桶 */
details.tier { margin: 10px 0 0; border: 1px solid #f0f3f7; border-radius: 10px; overflow: hidden; }
details.tier > summary {
  cursor: pointer; padding: 10px 14px; font-size: 13.5px; background: #fbfcfe;
  list-style: none; display: flex; align-items: center; gap: 8px; font-weight: 550;
}
details.tier > summary::-webkit-details-marker { display: none; }
details.tier > summary::before {
  content: ""; width: 5px; height: 5px; border-right: 1.4px solid #c3cbd5; border-bottom: 1.4px solid #c3cbd5;
  transform: rotate(-45deg); transition: transform .18s; flex: none;
}
details.tier[open] > summary::before { transform: rotate(45deg); }
details.tier > summary .count { color: #78828f; font-weight: 500; font-size: 12px; }
details.tier > summary:hover { background: #f6f9fc; }
.tier.mastered > summary { color: #9c6d12; }
.tier.familiar > summary { color: #266d8a; }
.tier.touch > summary { color: #4f6b85; }
.tier.none > summary { color: #59636f; }
.tier-body { padding: 6px 14px 12px; background: #fff; }

/* 节点行：名字 + 水位条 + 百分比 */
.item {
  display: grid; grid-template-columns: minmax(0, 1fr) 96px 46px;
  align-items: center; gap: 12px; padding: 8px 6px; border-radius: 8px;
  font-size: 14px; transition: background .12s;
}
.item:hover { background: #f8fafc; }
.item .name { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.item .bar { height: 7px; border-radius: 999px; background: #e7ecf2; overflow: hidden; }
.item .bar > i { display: block; height: 100%; border-radius: 999px; }
.item.mastered .bar > i { background: linear-gradient(90deg, #e5b93f, #b4780f); }
.item.familiar .bar > i { background: linear-gradient(90deg, #5fb3d2, #2b7fa3); }
.item.touch .bar > i { background: linear-gradient(90deg, #cfe4f0, #8fb8ce); }
.item.none .bar > i { background: #d3dae2; }
.item .val { text-align: right; font-size: 12.5px; color: #3f4a57; font-variant-numeric: tabular-nums; }
.item .val.zero { color: #8a94a3; }

.empty { text-align: center; color: var(--muted); padding: 48px 0; font-size: 14px; }
.footer { margin-top: 36px; text-align: center; color: #78828f; font-size: 12px; }
@media (prefers-reduced-motion: reduce) { * { transition: none !important; } }
</style>`)
	b.WriteString("</head>\n<body>\n")

	// ── 头部 ──
	b.WriteString("<div class=\"header\">\n")
	b.WriteString("<h1>知识掌握画像<span class=\"dot\">.</span></h1>\n")
	b.WriteString(fmt.Sprintf("<div class=\"subtitle\">知识库 <b>%s</b> · 导出时间 %s · 共 <b>%d</b> 个知识节点</div>\n",
		html.EscapeString(kbName), time.Now().Format("2006-01-02 15:04"), total))
	b.WriteString("</div>\n")

	// ── 概览：四档数字 + 分布条 ──
	b.WriteString("<div class=\"overview\">\n<div class=\"stats\">\n")
	for _, s := range []struct {
		tier string
		lbl  string
		n    int
	}{
		{"mastered", "掌握", masteredC},
		{"familiar", "熟悉", familiarC},
		{"touch", "接触", touchC},
		{"none", "不了解", noneC},
	} {
		b.WriteString(fmt.Sprintf(
			"<div class=\"stat %s\"><div class=\"num\">%d</div><div class=\"lbl\">%s</div><div class=\"pct\">%s%%</div></div>\n",
			s.tier, s.n, s.lbl, pctOf(s.n)))
	}
	b.WriteString("</div>\n")
	b.WriteString("<div class=\"distribution\">\n")
	for _, s := range []struct {
		tier string
		n    int
	}{{"mastered", masteredC}, {"familiar", familiarC}, {"touch", touchC}, {"none", noneC}} {
		if s.n == 0 {
			continue
		}
		b.WriteString(fmt.Sprintf("<div class=\"seg %s\" style=\"flex:%d 0 0\" title=\"%d 个节点\"></div>\n",
			s.tier, s.n, s.n))
	}
	b.WriteString("</div>\n")
	b.WriteString("<div class=\"distribution-legend\">\n")
	b.WriteString("<span><i style=\"background:linear-gradient(180deg,#f5d978,#c98b18)\"></i>掌握</span>\n")
	b.WriteString("<span><i style=\"background:linear-gradient(180deg,#8dcae0,#3c93b6)\"></i>熟悉</span>\n")
	b.WriteString("<span><i style=\"background:linear-gradient(180deg,#fff,#dce8f2);border:1px solid #e0e8ef\"></i>接触</span>\n")
	b.WriteString("<span><i style=\"background:#e3e8ee\"></i>不了解</span>\n")
	b.WriteString("</div>\n</div>\n")

	if total == 0 {
		b.WriteString("<div class=\"empty\">暂无行为证据。浏览、引用或点赞后，这里会生成你的知识掌握画像。</div>\n")
		b.WriteString("</body>\n</html>\n")
		return b.String()
	}

	// ── 页面类型桶（每个桶标题带一条迷你分布条） ──
	for _, pt := range pageTypeOrder {
		tiers, ok := byType[pt]
		if !ok || len(tiers) == 0 {
			continue
		}
		ptLabel := pageTypeLabelZH[pt]
		if ptLabel == "" {
			ptLabel = pt
		}
		typeTotal := h.countTypeNodes(tiers)
		b.WriteString(fmt.Sprintf("<details class=\"type\" open>\n<summary>%s <span class=\"count\">(%d)</span>%s</summary>\n<div class=\"type-body\">\n",
			ptLabel, typeTotal, miniDistribution(tiers, typeTotal)))
		for _, tier := range tierOrder {
			nodes := tiers[tier]
			if len(nodes) == 0 {
				continue
			}
			tierZH := masteryTierLabelZH[tier]
			if tierZH == "" {
				tierZH = tier
			}
			openAttr := ""
			if tier == "mastered" {
				openAttr = " open"
			}
			b.WriteString(fmt.Sprintf("<details class=\"tier %s\"%s>\n<summary>%s <span class=\"count\">(%d)</span></summary>\n<div class=\"tier-body\">\n",
				tier, openAttr, tierZH, len(nodes)))
			for _, n := range nodes {
				title := n.Title
				if title == "" {
					title = n.Slug
				}
				zeroCls := ""
				if n.Level <= 0 {
					zeroCls = " zero"
				}
				b.WriteString(fmt.Sprintf(
					"<div class=\"item %s\"><span class=\"name\" title=\"%s\">%s</span><span class=\"bar\"><i style=\"width:%d%%\"></i></span><span class=\"val%s\">%d%%</span></div>\n",
					tier, html.EscapeString(title), html.EscapeString(title), n.Level, zeroCls, n.Level))
			}
			b.WriteString("</div>\n</details>\n")
		}
		b.WriteString("</div>\n</details>\n")
	}

	b.WriteString("<div class=\"footer\">WeKnora · 知识网络与引导式学习 · 个人知识画像</div>\n")
	b.WriteString("</body>\n</html>\n")
	return b.String()
}

// miniDistribution renders a compact stacked bar for one page type, showing the
// tier mix at a glance in the bucket header.
func miniDistribution(tiers map[string][]types.MasteryNodeDetail, total int) string {
	if total == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("<span class=\"mini\">")
	for _, tier := range tierOrder {
		n := len(tiers[tier])
		if n == 0 {
			continue
		}
		b.WriteString(fmt.Sprintf("<i class=\"seg %s\" style=\"flex:%d 0 0\"></i>", tier, n))
	}
	b.WriteString("</span>")
	return b.String()
}

// countTypeNodes sums nodes across all tiers within one page type.
func (h *MasteryHandler) countTypeNodes(tiers map[string][]types.MasteryNodeDetail) int {
	n := 0
	for _, nodes := range tiers {
		n += len(nodes)
	}
	return n
}
