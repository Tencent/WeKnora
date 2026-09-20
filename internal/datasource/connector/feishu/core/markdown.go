package core

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
)

// sheetReader is the subset of *Client that blocksToMarkdown needs, so the
// converter can be unit-tested with a fake or nil client.
type sheetReader interface {
	readSheetRange(ctx context.Context, embedToken string) ([][]string, bool, error)
	readBitableRecords(ctx context.Context, embedToken string) ([][]string, bool, error)
	sheetMerges(ctx context.Context, embedToken string) ([]sheetMergeRange, error)
}

// pendingAttachment is an embedded file block awaiting a download decision by
// the connector (whitelist/size filtering happens there, not here).
type pendingAttachment struct {
	FileToken string
	Name      string
}

// pendingDocName is one 云文档 mention link awaiting a title backfill: the
// renderer emits "[weknora-docname://<token>](<url>)" placeholders (it has no
// access to document titles) and FetchDocxWithBlocks resolves the titles via
// the drive metadata batch API, patching the link text in place.
type pendingDocName struct {
	Token   string
	DocType string // drive metas doc_type: docx/doc/sheet/bitable/wiki/...
	URL     string
}

// pendingImage is one image/board marker emitted into the Markdown, in
// document order: N is the 1-based marker sequence number, Kind is the
// SubtreeChildID discriminator ("image" or "board"), Token the Feishu media or
// whiteboard token. The connector downloads each token and patches its marker
// in place — an inline base64 data URI on success, a plain ![图片]()
// placeholder on failure (no image sub-items are created).
type pendingImage struct {
	N     int
	Kind  string
	Token string
}

// mdRenderer carries per-document rendering state: the block index for
// container recursion, download candidates, and the ordered-list counter.
type mdRenderer struct {
	ctx         context.Context
	client      sheetReader
	byID        map[string]DocxBlock
	atts        []pendingAttachment
	imgs        []pendingImage
	docNames    []pendingDocName
	docNameSeen map[string]bool
	imgN        int    // last marker sequence number assigned
	markerNonce string // per-document nonce keeping user text from matching markers
	docURL      string // parent document's web URL, for non-whitelisted attachment placeholders

	// userName resolves a mention_user OpenID to a display name; "" means
	// unresolved (renders the generic @成员). Nil-safe via the default set in
	// blocksToMarkdown.
	userName func(string) string

	orderedActive     bool   // an ordered-list run is in progress
	lastOrderedParent string // ParentID of the most recent ordered item
	orderedNext       int    // next number for "auto"/absent sequence
}

// nextImageNo assigns the next marker sequence number. The renderer and the
// recorded pendingImage share this single counter, so the marker in the
// Markdown and the pendingImage can never disagree.
func (r *mdRenderer) nextImageNo(kind, token string) int {
	r.imgN++
	r.imgs = append(r.imgs, pendingImage{N: r.imgN, Kind: kind, Token: token})
	return r.imgN
}

// imageMarker renders the synthetic placeholder for image/board N. The nonce
// keeps user-typed look-alike text (a literal ![图片](weknora-img://1) in a
// text run) from ever matching the connector's patch needle — only this
// renderer and the patcher share the derivation (imageMarkerNonce).
func (r *mdRenderer) imageMarker(n int) string {
	return fmt.Sprintf("![图片](weknora-img://%d-%s)", n, r.markerNonce)
}

// imageMarkerNonce derives the per-document marker nonce from the doc URL.
func imageMarkerNonce(seed string) string {
	h := fnv.New32a()
	_, _ = h.Write([]byte(seed))
	return fmt.Sprintf("%08x", h.Sum32())
}

// blocksToMarkdown renders a flat docx block array to Markdown, inlining
// embedded spreadsheet/bitable tables (Task 5), collecting downloadable
// attachments, and numbering image/board blocks as weknora-img://N markers.
// docURL is the parent document's web URL used in non-whitelisted attachment
// placeholders (empty → placeholder without link). client may be nil when the
// block set has no downdrill blocks. userName resolves mention_user OpenIDs to
// display names (nil → generic @成员 for every mention; documents without
// mentions never invoke it, so it costs zero API calls).
func blocksToMarkdown(ctx context.Context, client sheetReader, blocks []DocxBlock, docURL string, userName func(string) string) ([]byte, []pendingAttachment, []pendingImage, []pendingDocName, error) {
	byID := make(map[string]DocxBlock, len(blocks))
	for _, b := range blocks {
		byID[b.BlockID] = b
	}
	// Blocks nested inside a native table are rendered by renderNativeTable;
	// blocks nested inside a container the main loop renders recursively
	// (callout / grid / quote_container) are rendered by that container. Mark
	// both so the flat loop below never Emits them a second time.
	consumed := tableDescendants(blocks, byID)
	markContainerDescendants(blocks, byID, consumed)

	if userName == nil {
		userName = func(string) string { return "" }
	}
	r := &mdRenderer{ctx: ctx, client: client, byID: byID, docURL: docURL, userName: userName, markerNonce: imageMarkerNonce(docURL)}
	var sb strings.Builder
	for _, b := range blocks {
		if consumed[b.BlockID] {
			continue
		}
		writePara(&sb, r.renderBlock(b))
	}
	out := strings.TrimRight(sb.String(), "\n")
	// listDocumentBlocks stops collecting at maxDocumentBlocks, so a full-size
	// array means content was dropped silently. Annotate it like the
	// embedded-table truncation does instead of failing the document.
	if len(blocks) >= maxDocumentBlocks {
		out += fmt.Sprintf("\n> 文档已截断（仅显示前 %d 个块）", maxDocumentBlocks)
	}
	if out != "" {
		out += "\n"
	}
	return []byte(out), r.atts, r.imgs, r.docNames, nil
}

// markContainerDescendants marks every block reachable through the children of
// the container blocks the main loop renders recursively (callout, grid — its
// columns' content is rendered per column — quote_container, and view).
func markContainerDescendants(blocks []DocxBlock, byID map[string]DocxBlock, consumed map[string]bool) {
	var mark func(id string)
	mark = func(id string) {
		b, ok := byID[id]
		if !ok || consumed[id] {
			return
		}
		consumed[id] = true
		for _, c := range b.Children {
			mark(c)
		}
	}
	for _, b := range blocks {
		switch b.BlockType {
		case BlockTypeCallout, BlockTypeGrid, BlockTypeQuoteContainer, BlockTypeView:
			for _, c := range b.Children {
				mark(c)
			}
		}
	}
}

// renderBlock renders one block, recursing into containers. Returns "" when
// the block contributes nothing.
func (r *mdRenderer) renderBlock(b DocxBlock) string {
	// Only a sibling of the last ordered item ends an ordered run. Ordered
	// items share one parent, while their child blocks render flat between
	// items carrying the item's own ID — those must not renumber the run.
	if b.BlockType != BlockTypeOrdered {
		if b.ParentID == r.lastOrderedParent {
			r.orderedActive = false
		}
	} else {
		r.lastOrderedParent = b.ParentID
	}
	switch b.BlockType {
	case BlockTypePage, BlockTypeTableCell:
		return "" // containers rendered elsewhere
	case BlockTypeText:
		return r.richText(textBearingField(b))
	case BlockTypeBullet:
		return "- " + r.richText(b.Bullet)
	case BlockTypeOrdered:
		return fmt.Sprintf("%d. %s", r.nextOrderedNo(textBearingField(b)), r.richText(b.Ordered))
	case BlockTypeCode:
		return r.renderCode(b)
	case BlockTypeQuote:
		return "> " + r.richText(b.Quote)
	case BlockTypeTodo:
		bt := textBearingField(b)
		if t := r.richText(bt); t != "" {
			if bt != nil && bt.Style != nil && bt.Style.Done {
				return "- [x] " + t
			}
			return "- [ ] " + t
		}
	case BlockTypeDivider:
		return "---"
	case BlockTypeCallout:
		return r.renderQuotedContainer(b, r.richText(b.Callout))
	case BlockTypeQuoteContainer:
		return r.renderQuotedContainer(b, "")
	case BlockTypeGrid:
		return r.renderGrid(b)
	case BlockTypeView:
		// view is a pure container (e.g. the file-list "视图" wrapping file
		// blocks): render its children inline, no placeholder. An empty view
		// contributes nothing.
		var lines []string
		for _, cid := range b.Children {
			if child, ok := r.byID[cid]; ok {
				if s := r.renderBlock(child); s != "" {
					lines = append(lines, strings.Split(s, "\n")...)
				}
			}
		}
		return strings.Join(lines, "\n")
	case BlockTypeTable:
		return renderNativeTable(b, r.byID)
	case BlockTypeSheet:
		if b.Sheet != nil {
			return inlineTable(r.ctx, r.client, b.Sheet.Token, "sheet")
		}
	case BlockTypeBitable:
		if b.Bitable != nil {
			return inlineTable(r.ctx, r.client, b.Bitable.Token, "bitable")
		}
	case BlockTypeImage:
		// Token-free numbered marker: the media token never enters the Markdown;
		// the connector downloads it and patches the marker in place.
		var tok string
		if b.Image != nil {
			tok = b.Image.Token
		}
		n := r.nextImageNo("image", tok)
		return r.imageMarker(n)
	case BlockTypeBoard:
		if b.Board == nil || b.Board.Token == "" {
			return unsupportedBlockNote(b)
		}
		// Boards ride the same image pipeline: the connector downloads the
		// whiteboard export and inlines it. On failure it patches this marker
		// back to a plain placeholder.
		n := r.nextImageNo("board", b.Board.Token)
		return r.imageMarker(n)
	case BlockTypeFile:
		if b.File != nil {
			name := b.File.Name
			if name == "" {
				name = b.File.Token
			}
			if parseableAttachmentExts[strings.ToLower(filepath.Ext(name))] {
				r.atts = append(r.atts, pendingAttachment{FileToken: b.File.Token, Name: b.File.Name})
				// Unique token line, not the display name: the connector swaps
				// this exact line after the download attempt (name on success /
				// tiny, degrade note over cap), so an identical earlier bullet
				// can never be replaced by mistake.
				return "- weknora-att://" + b.File.Token
			}
			// Not ingestible (wrong type / video): keep a visible reference with
			// the source link when available.
			if r.docURL != "" {
				return "> [附件: " + name + "](" + escapeURL(r.docURL) + ")"
			}
			return "> [附件: " + name + "]"
		}
	case BlockTypeIframe:
		if b.Iframe != nil && b.Iframe.Component != nil && b.Iframe.Component.URL != "" {
			return "[内嵌网页](" + escapeURL(b.Iframe.Component.URL) + ")"
		}
		return unsupportedBlockNote(b)
	case BlockTypeAddOns:
		// 文档小组件 carry their full data inline in `record` (JSON string);
		// there is no server-side render to download. Recognized component
		// types degrade to structured Markdown, unknown ones keep the note.
		if b.AddOns != nil && b.AddOns.ComponentTypeID == addOnsTimelineTypeID {
			if md := renderTimelineAddOn(b.AddOns.Record); md != "" {
				return md
			}
		}
		return unsupportedBlockNote(b)
	default:
		if b.BlockType >= BlockTypeHeading1 && b.BlockType <= blockTypeHeading9 {
			level := b.BlockType - BlockTypeHeading1 + 1
			if level <= 6 {
				return strings.Repeat("#", level) + " " + r.richText(headingText(b))
			}
			// GFM headings stop at H6; Feishu heading7-9 degrade to bold text
			// (a 7-hash "heading" is plain text to every Markdown renderer).
			return "**" + r.richText(headingText(b)) + "**"
		}
		return unsupportedBlockNote(b)
	}
	return ""
}

// renderQuotedContainer renders a container block (callout / quote_container):
// its own inline text (if any) plus each child block, every line prefixed with
// "> ".
func (r *mdRenderer) renderQuotedContainer(b DocxBlock, own string) string {
	var lines []string
	if own != "" {
		lines = append(lines, own)
	}
	for _, cid := range b.Children {
		child, ok := r.byID[cid]
		if !ok {
			continue
		}
		if s := r.renderBlock(child); s != "" {
			lines = append(lines, strings.Split(s, "\n")...)
		}
	}
	if len(lines) == 0 {
		return ""
	}
	for i, l := range lines {
		lines[i] = "> " + l
	}
	return strings.Join(lines, "\n")
}

// renderGrid renders a 分栏 (grid) block: each child column's content is
// rendered in column order, columns separated by a horizontal rule.
func (r *mdRenderer) renderGrid(b DocxBlock) string {
	var cols []string
	for _, cid := range b.Children {
		col, ok := r.byID[cid]
		if !ok {
			continue
		}
		var parts []string
		if col.BlockType == BlockTypeGridColumn {
			for _, ccid := range col.Children {
				if cb, ok := r.byID[ccid]; ok {
					if s := r.renderBlock(cb); s != "" {
						parts = append(parts, s)
					}
				}
			}
		} else if s := r.renderBlock(col); s != "" {
			// Malformed grid whose child is not a grid_column: keep its content.
			parts = append(parts, s)
		}
		if len(parts) > 0 {
			cols = append(cols, strings.Join(parts, "\n\n"))
		}
	}
	return strings.Join(cols, "\n\n---\n\n")
}

// nextOrderedNo returns the real number for an ordered list item. Feishu stores
// it in style.sequence: a specific value starts or restarts the list ("3"),
// "auto" continues it. Historical/OpenAPI-created docs omit the field entirely;
// there we fall back to relative numbering, incrementing until a non-ordered
// block resets the run.
func (r *mdRenderer) nextOrderedNo(bt *BlockText) int {
	if bt != nil && bt.Style != nil {
		if n, err := strconv.Atoi(bt.Style.Sequence); err == nil && n > 0 {
			r.orderedActive = true
			r.orderedNext = n + 1
			return n
		}
	}
	if !r.orderedActive {
		r.orderedActive = true
		r.orderedNext = 2
		return 1
	}
	n := r.orderedNext
	r.orderedNext++
	return n
}

// writePara appends a block of text followed by a blank line; empty text is skipped.
func writePara(sb *strings.Builder, s string) {
	if s == "" {
		return
	}
	sb.WriteString(s)
	sb.WriteString("\n\n")
}

// plainText concatenates the raw text runs of a text-bearing block (no inline
// styling — used for table cells, where GFM styling would break the row).
func plainText(bt *BlockText) string {
	if bt == nil {
		return ""
	}
	var sb strings.Builder
	for _, e := range bt.Elements {
		if e.TextRun != nil {
			sb.WriteString(e.TextRun.Content)
		}
	}
	return sb.String()
}

// mentionDocTypeByPathSegment maps Feishu URL path segments to drive metas
// doc_type values. Wiki links resolve through this table too — the metadata
// API supports wiki tokens for titles (content fetching is another matter).
var mentionDocTypeByPathSegment = map[string]string{
	"doc":      "doc",
	"docx":     "docx",
	"sheets":   "sheet",
	"base":     "bitable",
	"wiki":     "wiki",
	"mindnote": "mindnote",
	"slides":   "slides",
	"file":     "file",
}

// parseMentionDocRef extracts (token, doc_type) from a Feishu document URL
// like https://x.feishu.cn/docx/<token>. Returns ok=false for anything that
// is not a recognizable Feishu doc URL (the link then stays as-is).
func parseMentionDocRef(u string) (pendingDocName, bool) {
	parsed, err := url.Parse(u)
	if err != nil {
		return pendingDocName{}, false
	}
	segs := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	for i, seg := range segs {
		docType, ok := mentionDocTypeByPathSegment[seg]
		if !ok || i+1 >= len(segs) {
			continue
		}
		token := segs[i+1]
		if token == "" {
			continue
		}
		for _, r := range token {
			if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9') {
				return pendingDocName{}, false
			}
		}
		return pendingDocName{Token: token, DocType: docType, URL: u}, true
	}
	return pendingDocName{}, false
}

// escapeURL renders a Feishu-provided URL as a safe Markdown href. The docx
// API percent-encodes link targets (e.g. http%3A%2F%2F...), so that encoding
// is reversed first — a value that does not decode cleanly passes through
// untouched — and only then are characters that would break the inline-link
// syntax re-escaped.
func escapeURL(u string) string {
	if d, err := url.PathUnescape(u); err == nil {
		u = d
	}
	r := strings.NewReplacer(" ", "%20", ")", "%29", "(", "%28")
	return r.Replace(u)
}

func (r *mdRenderer) richText(bt *BlockText) string {
	if bt == nil {
		return ""
	}
	var sb strings.Builder
	for _, e := range bt.Elements {
		switch {
		case e.TextRun != nil:
			sb.WriteString(styleRun(e.TextRun.Content, e.TextRun.TextElementStyle))
		case e.MentionDoc != nil && e.MentionDoc.URL != "":
			// The payload carries no document title. Feishu doc URLs emit a
			// title placeholder that FetchDocxWithBlocks backfills via the
			// drive metadata API; anything else keeps the URL as link text.
			u := e.MentionDoc.URL
			if ref, ok := parseMentionDocRef(u); ok {
				if r.docNameSeen == nil {
					r.docNameSeen = map[string]bool{}
				}
				if !r.docNameSeen[ref.Token] {
					r.docNameSeen[ref.Token] = true
					r.docNames = append(r.docNames, ref)
				}
				sb.WriteString("[weknora-docname://" + ref.Token + "](" + escapeURL(u) + ")")
			} else {
				sb.WriteString("[" + u + "](" + escapeURL(u) + ")")
			}
		case e.MentionUser != nil:
			// The API carries only the user OpenID; the caller-supplied
			// resolver (cached, degrading) turns it into a display name when
			// it can. Unresolved → generic @成员, never an error.
			if name := r.userName(e.MentionUser.UserID); name != "" {
				sb.WriteString("@" + name)
			} else {
				sb.WriteString("@成员")
			}
		case e.Equation != nil && strings.TrimSpace(e.Equation.Content) != "":
			sb.WriteString("$" + strings.TrimSpace(e.Equation.Content) + "$")
		}
	}
	return sb.String()
}

// styleRun wraps a text run in the GFM markers of its inline style.
func styleRun(content string, st *TextElementStyle) string {
	if content == "" {
		return ""
	}
	if st == nil {
		return content
	}
	if st.InlineCode {
		content = "`" + content + "`"
	}
	if st.Bold {
		content = "**" + content + "**"
	}
	if st.Italic {
		content = "*" + content + "*"
	}
	if st.Strikethrough {
		content = "~~" + content + "~~"
	}
	if st.Link != nil && st.Link.URL != "" {
		content = "[" + content + "](" + escapeURL(st.Link.URL) + ")"
	}
	return content
}

// gfmCodeLanguages maps the Feishu CodeLanguage enum (1-75, official docx-v1
// docs) to GFM fence info strings. 1 (PlainText) and unknown values render an
// untagged fence.
var gfmCodeLanguages = map[int]string{
	2: "abap", 3: "ada", 4: "apache", 5: "apex", 6: "asm", 7: "bash",
	8: "c#", 9: "cpp", 10: "c", 11: "cobol", 12: "css", 13: "coffeescript",
	14: "d", 15: "dart", 16: "delphi", 17: "django", 18: "dockerfile",
	19: "erlang", 20: "fortran", 21: "foxpro", 22: "go", 23: "groovy",
	24: "html", 25: "htmlbars", 26: "http", 27: "haskell", 28: "json",
	29: "java", 30: "javascript", 31: "julia", 32: "kotlin", 33: "latex",
	34: "lisp", 35: "logo", 36: "lua", 37: "matlab", 38: "makefile",
	39: "markdown", 40: "nginx", 41: "objective-c", 42: "openedgeabl",
	43: "php", 44: "perl", 45: "postscript", 46: "powershell", 47: "prolog",
	48: "protobuf", 49: "python", 50: "r", 51: "rpg", 52: "ruby", 53: "rust",
	54: "sas", 55: "scss", 56: "sql", 57: "scala", 58: "scheme", 59: "scratch",
	60: "shell", 61: "swift", 62: "thrift", 63: "typescript", 64: "vbscript",
	65: "vb", 66: "xml", 67: "yaml", 68: "cmake", 69: "diff", 70: "gherkin",
	71: "graphql", 72: "glsl", 73: "properties", 74: "solidity", 75: "toml",
}

// renderCode renders a code block, tagging the fence with the GFM alias of the
// block's CodeLanguage when one is known. The fence grows past any backtick
// run inside the content so a line of ``` in the code cannot close it early.
func (r *mdRenderer) renderCode(b DocxBlock) string {
	lang := ""
	if b.Code != nil && b.Code.Style != nil {
		lang = gfmCodeLanguages[b.Code.Style.Language]
	}
	content := r.richText(b.Code)
	n := codeFenceLength(content)
	open := strings.Repeat("`", n)
	if lang != "" {
		open += lang
	}
	// CommonMark: the closing fence carries no info string — reusing the
	// opening fence (with language) would leave the block unterminated.
	return open + "\n" + content + "\n" + strings.Repeat("`", n)
}

// codeFenceLength returns a fence length strictly longer than the longest
// leading backtick run on any content line (≥3), per the CommonMark fencing rule.
func codeFenceLength(content string) int {
	longest := 2
	for _, line := range strings.Split(content, "\n") {
		n := 0
		for n < len(line) && line[n] == '`' {
			n++
		}
		if n > longest {
			longest = n
		}
	}
	return longest + 1
}

// blockTypeNames maps docx block types this converter does not unpack to their
// names, for the degraded placeholder line. Board (43) is only reached here when
// its block carries no token (the token case rides the image pipeline);
// mindnote (29) has no content API at all (token-only placeholder per Feishu
// docs).
var blockTypeNames = map[int]string{
	BlockTypeChatCard:          "会话卡片",
	BlockTypeDiagram:           "流程图&UML",
	BlockTypeGridColumn:        "分栏列",
	BlockTypeISV:               "开放平台小组件",
	BlockTypeMindnote:          "思维笔记",
	BlockTypeTask:              "任务",
	BlockTypeOKR:               "OKR",
	BlockTypeOKRObjective:      "OKR目标",
	BlockTypeOKRKeyResult:      "OKR关键结果",
	BlockTypeOKRProgress:       "OKR进展",
	BlockTypeAddOns:            "文档小组件",
	BlockTypeJiraIssue:         "Jira问题",
	BlockTypeWikiCatalog:       "Wiki子页面列表",
	BlockTypeBoard:             "画板",
	BlockTypeAgenda:            "议程",
	BlockTypeAgendaItem:        "议程项",
	BlockTypeAgendaItemTitle:   "议程项标题",
	BlockTypeAgendaItemContent: "议程项内容",
	BlockTypeLinkPreview:       "链接预览",
	BlockTypeSourceSynced:      "源同步块",
	BlockTypeReferenceSynced:   "引用同步块",
	BlockTypeSubPageList:       "Wiki子页面列表(新版)",
	BlockTypeAITemplate:        "AI模板",
}

// unsupportedBlockNote renders the degraded placeholder for a block type this
// converter does not unpack. Tokens are never included (no-leak constraint).
func unsupportedBlockNote(b DocxBlock) string {
	if name, ok := blockTypeNames[b.BlockType]; ok {
		return "> [飞书块: " + name + "]"
	}
	return fmt.Sprintf("> [飞书块: 未知类型(%d)]", b.BlockType)
}

// headingText returns the heading field for the block's level, or Text as fallback.
func headingText(b DocxBlock) *BlockText {
	fields := []*BlockText{
		b.Heading1, b.Heading2, b.Heading3, b.Heading4, b.Heading5,
		b.Heading6, b.Heading7, b.Heading8, b.Heading9,
	}
	if idx := b.BlockType - BlockTypeHeading1; idx >= 0 && idx < len(fields) && fields[idx] != nil {
		return fields[idx]
	}
	return b.Text
}

// tableDescendants returns the set of block IDs that belong to a native table —
// every cell listed in a table block plus everything reachable through those
// cells' Children. blocksToMarkdown skips these so table content is emitted only
// by the table renderer, never a second time as loose paragraphs.
func tableDescendants(blocks []DocxBlock, byID map[string]DocxBlock) map[string]bool {
	consumed := make(map[string]bool)
	var mark func(id string)
	mark = func(id string) {
		b, ok := byID[id]
		if !ok || consumed[id] {
			return
		}
		// Attachment/media blocks nested in a cell must still be collected (and
		// their reference emitted) by the main loop — the table renderer only
		// extracts text — so do not consume them, only their text structure.
		if b.BlockType == BlockTypeFile || b.BlockType == BlockTypeImage {
			return
		}
		consumed[id] = true
		for _, c := range b.Children {
			mark(c)
		}
	}
	for _, b := range blocks {
		// Only consume cells of tables we will actually render. A table that
		// renderNativeTable would bail on (missing property / zero columns) must
		// NOT have its cells consumed, or their text would be dropped entirely —
		// leave them for the flat loop to Emit as loose paragraphs instead.
		if b.BlockType == BlockTypeTable && tableRenderable(b) {
			for _, cid := range b.Table.Cells {
				mark(cid)
			}
		}
	}
	return consumed
}

// tableRenderable reports whether a native table block carries enough structure
// (a column count) for renderNativeTable to produce a Markdown table. It is the
// single predicate shared by the consume pass and the render pass so the two
// never disagree about which tables are handled by the table renderer.
func tableRenderable(b DocxBlock) bool {
	return b.Table != nil && b.Table.Property != nil && b.Table.Property.ColumnSize > 0
}

// textBearingField returns the inline-text payload for whichever type-named
// field a block populates (docx stores a block's text in a field named after
// its type), so cell content of any text-like type can be extracted uniformly.
func textBearingField(b DocxBlock) *BlockText {
	switch b.BlockType {
	case BlockTypeText:
		return b.Text
	case BlockTypeBullet:
		return b.Bullet
	case BlockTypeOrdered:
		return b.Ordered
	case BlockTypeCode:
		return b.Code
	case BlockTypeQuote:
		return b.Quote
	case BlockTypeTodo:
		return b.Todo
	case BlockTypeCallout:
		return b.Callout
	}
	if b.BlockType >= BlockTypeHeading1 && b.BlockType <= blockTypeHeading9 {
		return headingText(b)
	}
	return b.Text
}

// cellTextAndAlign renders a native table cell to a single string and reports
// the cell's column alignment (0 when default). A Feishu table_cell
// (block_type 32) is a container: its text lives in child blocks, not on the
// cell itself, so we concatenate the text of each child block; the first child
// carrying an explicit style alignment (2 center / 3 right) wins.
func cellTextAndAlign(cell DocxBlock, byID map[string]DocxBlock) (string, int) {
	var parts []string
	align := 0
	for _, childID := range cell.Children {
		child := byID[childID]
		if t := plainText(textBearingField(child)); t != "" {
			parts = append(parts, t)
		}
		if align == 0 && child.Text != nil && child.Text.Style != nil && child.Text.Style.Align != 0 {
			align = child.Text.Style.Align
		}
	}
	return strings.Join(parts, " "), align
}

// renderNativeTable renders a native docx table block into a Markdown table.
func renderNativeTable(b DocxBlock, byID map[string]DocxBlock) string {
	if !tableRenderable(b) {
		return ""
	}
	cols := b.Table.Property.ColumnSize
	var cells []string
	aligns := make([]int, cols)
	for i, cid := range b.Table.Cells {
		text, align := cellTextAndAlign(byID[cid], byID)
		cells = append(cells, text)
		if col := i % cols; align != 0 && aligns[col] == 0 {
			aligns[col] = align
		}
	}
	var rows [][]string
	for i := 0; i < len(cells); i += cols {
		end := i + cols
		if end > len(cells) {
			end = len(cells)
		}
		rows = append(rows, cells[i:end])
	}
	fillMergedCells(rows, cols, b.Table.Property.MergeInfo)
	return markdownTable(rows, aligns)
}

// fillMergedCells propagates each merged region's top-left value into the cells
// it covers — GFM has no row/col span, and dropping the covered cells' content
// would lose text, so the anchor value fills them instead. merge_info entries
// are parallel to the table's cells array; entries beyond the rendered grid are
// ignored. (Embedded sheet merge ranges come from the v3 metadata API instead —
// see fillSheetMerges; bitable has no merge concept.)
func fillMergedCells(rows [][]string, cols int, merge []BlockTableMergeInfo) {
	if len(merge) == 0 || len(rows) == 0 {
		return
	}
	cellAt := func(r, c int) string {
		if r < 0 || r >= len(rows) || c < 0 || c >= len(rows[r]) {
			return ""
		}
		return rows[r][c]
	}
	for i, mi := range merge {
		if mi.RowSpan <= 1 && mi.ColSpan <= 1 {
			continue
		}
		r0, c0 := i/cols, i%cols
		v := cellAt(r0, c0)
		for dr := range mi.RowSpan {
			for dc := range mi.ColSpan {
				if dr == 0 && dc == 0 {
					continue
				}
				rr, cc := r0+dr, c0+dc
				if rr < len(rows) && cc < len(rows[rr]) {
					rows[rr][cc] = v
				}
			}
		}
	}
}

// fillSheetMerges propagates each embedded-sheet merged region's top-left value
// across the whole region — same GFM-no-span rationale as fillMergedCells. The
// v3 metadata API reports 0-based CLOSED intervals [start..end]; regions beyond
// the rendered (row-capped) grid are clamped or skipped. If a real-world check
// shows one cell too many per region, the intervals are half-open — switch
// `i <= endRow` / `j <= endCol` to `<`.
func fillSheetMerges(rows [][]string, merges []sheetMergeRange) {
	for _, m := range merges {
		startRow, startCol := int(m.StartRow), int(m.StartCol)
		if startRow < 0 || startRow >= len(rows) || startCol < 0 || startCol >= len(rows[startRow]) {
			continue
		}
		anchor := rows[startRow][startCol]
		endRow := min(int(m.EndRow), len(rows)-1)
		for i := startRow; i <= endRow; i++ {
			endCol := min(int(m.EndCol), len(rows[i])-1)
			for j := startCol; j <= endCol; j++ {
				rows[i][j] = anchor
			}
		}
	}
}

// markdownTable renders a [][]string (first row = header) as a GFM table.
// aligns carries per-column alignment (0/1 left, 2 center, 3 right — the docx
// style.align values) rendered as GFM colons; nil or zero entries yield the
// plain left-aligned separator.
func markdownTable(rows [][]string, aligns []int) string {
	// A zero-column header (an embedded sheet/bitable with no columns) would Emit
	// a header line of "|  |" and a separator of just "|" — malformed GFM. Render
	// nothing instead.
	if len(rows) == 0 || len(rows[0]) == 0 {
		return ""
	}
	sep := func(col int) string {
		switch {
		case col < len(aligns) && aligns[col] == 2:
			return ":---:"
		case col < len(aligns) && aligns[col] == 3:
			return "---:"
		default:
			return "---"
		}
	}
	var sb strings.Builder
	cols := len(rows[0])
	sb.WriteString("| " + strings.Join(escapePipes(rows[0]), " | ") + " |\n|")
	for c := range cols {
		sb.WriteString(" " + sep(c) + " |")
	}
	sb.WriteString("\n")
	for _, r := range rows[1:] {
		// Ragged data: a row wider than the header would Emit more cells than the
		// header/separator declare, producing a malformed GFM table. Clamp to the
		// header width (truncate overflow, pad shortfall).
		if len(r) > cols {
			r = r[:cols]
		}
		for len(r) < cols {
			r = append(r, "")
		}
		sb.WriteString("| " + strings.Join(escapePipes(r), " | ") + " |\n")
	}
	return strings.TrimRight(sb.String(), "\n")
}

// addOnsTimelineTypeID is the component_type_id of the timeline 文档小组件.
// Add-on blocks are client-rendered from their inline `record` JSON — there
// is no server asset to download — so recognized types degrade to structured
// Markdown.
const addOnsTimelineTypeID = "blk_6358a421bca0001c22536e4c"

type addOnsTimelineRecord struct {
	ContentShow struct {
		Text  bool `json:"text"`
		Time  bool `json:"time"`
		Title bool `json:"title"`
	} `json:"contentShow"`
	Items []struct {
		Text  string `json:"text"`
		Time  string `json:"time"`
		Title string `json:"title"`
	} `json:"items"`
}

// renderTimelineAddOn renders a timeline widget's record as a Markdown list:
// `- **time · title**：text`, omitting the segments the widget hides
// (contentShow) or that are empty. Returns "" for malformed or empty records
// (the caller falls back to the placeholder note).
func renderTimelineAddOn(record string) string {
	var rec addOnsTimelineRecord
	if json.Unmarshal([]byte(record), &rec) != nil || len(rec.Items) == 0 {
		return ""
	}
	lines := make([]string, 0, len(rec.Items))
	for _, it := range rec.Items {
		var head []string
		if rec.ContentShow.Time && it.Time != "" {
			head = append(head, it.Time)
		}
		if rec.ContentShow.Title && it.Title != "" {
			head = append(head, it.Title)
		}
		line := "-"
		if len(head) > 0 {
			line = "- **" + strings.Join(head, " · ") + "**"
		}
		if rec.ContentShow.Text && it.Text != "" {
			if len(head) > 0 {
				line += "：" + it.Text
			} else {
				line += " " + it.Text
			}
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n\n")
}

// escapePipes makes cell values safe for a single Markdown table row.
func escapePipes(row []string) []string {
	out := make([]string, len(row))
	for i, c := range row {
		out[i] = strings.ReplaceAll(strings.ReplaceAll(c, "\n", " "), "|", "\\|")
	}
	return out
}

// inlineTable reads an embedded sheet/bitable and renders it as a Markdown
// table. Read/permission errors degrade to an inline note rather than failing
// the whole document (partial content beats no content). A reader-reported
// truncation appends a note.
func inlineTable(ctx context.Context, client sheetReader, token, kind string) string {
	if client == nil {
		return ""
	}
	var (
		rows      [][]string
		truncated bool
		err       error
		noun      string
	)
	if kind == "sheet" {
		rows, truncated, err = client.readSheetRange(ctx, token)
		noun = "内嵌电子表格"
	} else {
		rows, truncated, err = client.readBitableRecords(ctx, token)
		noun = "内嵌多维表格"
	}
	if err != nil {
		return fmt.Sprintf("> [无法读取%s]", noun)
	}
	// Embedded sheets (unlike native tables) only expose merge regions through
	// the v3 metadata API; fetch them for the fill below. A failure degrades to
	// the raw values — merge info is cosmetic. Bitable has no merge concept.
	if kind == "sheet" {
		if merges, merr := client.sheetMerges(ctx, token); merr == nil && len(merges) > 0 {
			fillSheetMerges(rows, merges)
		}
	}
	// markdownTable returns "" when there is nothing renderable (no rows, or a
	// header with no columns). Skip the truncation note too in that case, since it
	// would otherwise dangle without a table above it.
	table := markdownTable(rows, nil) // sheet/bitable values carry no alignment
	if table == "" {
		return ""
	}
	if truncated {
		table += fmt.Sprintf("\n\n> 表格已截断（仅显示前 %d 行）", maxTableRows)
	}
	return table
}
