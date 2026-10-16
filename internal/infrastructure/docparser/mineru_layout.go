package docparser

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"html"
	"math"
	"regexp"
	"sort"
	"strings"

	"github.com/Tencent/WeKnora/internal/sourceloc"
	"github.com/Tencent/WeKnora/internal/types"
)

// minerUBBoxScale is the coordinate range of content_list bounding boxes:
// MinerU normalizes them to 0-1000 of the page, origin at the top-left.
const minerUBBoxScale = 1000.0

// minerUContentItem is one layout block of MinerU's content_list output.
type minerUContentItem struct {
	Type         string          `json:"type"`
	Text         string          `json:"text"`
	PageIdx      *int            `json:"page_idx"`
	BBox         []float64       `json:"bbox"`
	TableBody    string          `json:"table_body"`
	TableCaption json.RawMessage `json:"table_caption"`
	ImageCaption json.RawMessage `json:"image_caption"`
	ListItems    json.RawMessage `json:"list_items"`
	CodeBody     string          `json:"code_body"`
}

var minerUHTMLTagRe = regexp.MustCompile(`<[^>]+>`)

// text is the block's searchable text, in the order MinerU renders it to
// markdown: captions above tables and figures, then the body.
func (it minerUContentItem) text() string {
	parts := []string{it.Text}
	parts = append(parts, rawStrings(it.TableCaption)...)
	parts = append(parts, rawStrings(it.ImageCaption)...)
	if it.TableBody != "" {
		parts = append(parts, minerUHTMLTagRe.ReplaceAllString(it.TableBody, " "))
	}
	parts = append(parts, it.CodeBody)
	parts = append(parts, rawStrings(it.ListItems)...)
	return strings.TrimSpace(strings.Join(parts, " "))
}

// rawStrings reads a JSON string or array of strings.
func rawStrings(raw json.RawMessage) []string {
	if len(raw) == 0 {
		return nil
	}
	var list []string
	if json.Unmarshal(raw, &list) == nil {
		return list
	}
	var one string
	if json.Unmarshal(raw, &one) == nil {
		return []string{one}
	}
	return nil
}

// decodeMinerUContentList accepts the list itself or the list serialized as
// a JSON string, which is how some MinerU API versions return it.
func decodeMinerUContentList(raw []byte) []minerUContentItem {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return nil
	}
	if raw[0] == '"' {
		var inner string
		if json.Unmarshal(raw, &inner) != nil {
			return nil
		}
		raw = []byte(inner)
	}
	var items []minerUContentItem
	if json.Unmarshal(raw, &items) != nil {
		return nil
	}
	return items
}

// minerUContentListFromZip prefers supported MiddleJson 2.0 geometry and
// falls back to legacy content_list.json for older MinerU packages.
func minerUContentListFromZip(zipData []byte) []byte {
	zr, err := zip.NewReader(bytes.NewReader(zipData), int64(len(zipData)))
	if err != nil {
		return nil
	}
	var candidates []*zip.File
	for _, f := range zr.File {
		name := strings.ToLower(f.Name)
		if strings.HasSuffix(name, "middle_json.json") || strings.HasSuffix(name, "content_list.json") {
			candidates = append(candidates, f)
		}
	}
	if len(candidates) == 0 {
		return nil
	}
	sort.Slice(candidates, func(i, j int) bool {
		mi,
			mj := strings.HasSuffix(strings.ToLower(candidates[i].Name),
			"middle_json.json"),
			strings.HasSuffix(strings.ToLower(candidates[j].Name),
				"middle_json.json")
		if mi != mj {
			return mi
		}
		di, dj := strings.Count(candidates[i].Name, "/"), strings.Count(candidates[j].Name, "/")
		if di != dj {
			return di < dj
		}
		return candidates[i].Name < candidates[j].Name
	})
	for _, candidate := range candidates {
		data, err := readZipEntryBytes(candidate)
		if err != nil {
			continue
		}
		if strings.HasSuffix(strings.ToLower(candidate.Name), "middle_json.json") {
			var doc minerUMiddle
			if json.Unmarshal(data, &doc) != nil || doc.Schema != "docvortex.middle" || doc.Version != "2.0" {
				continue
			}
		}
		return data
	}
	return nil
}

// minerUSourceBlocks aligns MinerU's layout blocks against its markdown so
// each paragraph, table and figure points back at its page and region. Only
// PDFs and images have pages a viewer can show; for office files MinerU's
// pages belong to an intermediate PDF, so those are left to the structure
// aligner.
func minerUSourceBlocks(markdown string, contentList []byte, fileType string) []types.SourceBlock {
	ft := strings.ToLower(strings.TrimPrefix(fileType, "."))
	if ft != "pdf" && !IsImageFormat(ft) {
		return nil
	}
	raw := bytes.TrimSpace(contentList)
	if len(raw) > 0 && raw[0] == '{' {
		return minerUMiddleSourceBlocks(markdown, raw)
	}
	items := decodeMinerUContentList(contentList)
	if len(items) == 0 {
		return nil
	}
	units := make([]sourceloc.Unit, 0, len(items))
	for i, it := range items {
		if it.PageIdx == nil || *it.PageIdx < 0 {
			continue
		}
		loc := types.SourceLocator{
			Type: types.SourceLocatorPDF,
			Page: *it.PageIdx + 1,
			SourceID: fmt.Sprintf("mineru:legacy:%d:%d",
				*it.PageIdx,
				i),
		}
		if bbox, ok := minerUBBox(it.BBox); ok {
			loc.BBox = bbox
		}
		units = append(units, sourceloc.Unit{Text: it.text(), Locator: loc})
	}
	return sourceloc.Align(markdown, units)
}

// minerUBBox converts a 0-1000 content_list box to page fractions. Boxes
// outside that range come from MinerU versions that report other units and
// are dropped rather than drawn in the wrong place.
func minerUBBox(b []float64) ([]float64, bool) {
	if len(b) != 4 || b[2] <= b[0] || b[3] <= b[1] {
		return nil, false
	}
	for _, v := range b {
		if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 || v > minerUBBoxScale {
			return nil, false
		}
	}
	out := make([]float64, 4)
	for i, v := range b {
		out[i] = v / minerUBBoxScale
	}
	return out, true
}

// MiddleJson 2.0 uses [0,1] normalized boxes (NOT legacy 0..1000).
// Child annotations keep their own geometry. Inline spans have no geometry.
type minerUMiddleBlock struct {
	Type    string          `json:"type"`
	Index   *int            `json:"index"`
	BBox    []float64       `json:"bbox"`
	Content json.RawMessage `json:"content"`
}

type minerUMiddle struct {
	Schema  string `json:"schema"`
	Version string `json:"schema_version"`
	Pages   []struct {
		PageIdx *int                `json:"page_idx"`
		Blocks  []minerUMiddleBlock `json:"blocks"`
	} `json:"pages"`
}

func minerUMiddleSourceBlocks(markdown string, raw []byte) []types.SourceBlock {
	var doc minerUMiddle
	if json.Unmarshal(raw, &doc) != nil || doc.Schema != "docvortex.middle" || doc.Version != "2.0" {
		return nil
	}
	var units []sourceloc.Unit
	previousPage := -1
	for _, page := range doc.Pages {
		if page.PageIdx == nil || *page.PageIdx <= previousPage {
			return nil
		}
		previousPage = *page.PageIdx
		previousBlock := -1
		for _, block := range page.Blocks {
			if block.Index == nil || *block.Index <= previousBlock {
				return nil
			}
			previousBlock = *block.Index
			ref := fmt.Sprintf("mineru:2:%d:%d", *page.PageIdx, *block.Index)
			units = append(units, minerUMiddleUnits(block, *page.PageIdx+1, ref, nil)...)
		}
	}
	return sourceloc.Align(markdown, units)
}

func minerUMiddleUnits(block minerUMiddleBlock, page int, ref string, parentBox []float64) []sourceloc.Unit {
	box := block.BBox
	if len(box) == 0 {
		box = parentBox
	}
	loc := types.SourceLocator{Type: types.SourceLocatorPDF, Page: page, SourceID: ref}
	if normalizedMinerUBox(box) {
		loc.BBox = append([]float64(nil), box...)
	}
	// These containers contain blocks; text and hyperlinks contain inline spans.
	switch block.Type {
	case "list", "index", "table", "image", "chart", "code":
		var children []minerUMiddleBlock
		if json.Unmarshal(block.Content, &children) != nil {
			return nil
		}
		var out []sourceloc.Unit
		for i, child := range children {
			out = append(out, minerUMiddleUnits(child, page, fmt.Sprintf("%s/%d", ref, i), box)...)
		}
		return out
	default:
		text := minerUMiddleText(block.Content)
		if block.Type == "table_body" || block.Type == "chart_body" {
			text = html.UnescapeString(minerUHTMLTagRe.ReplaceAllString(text, " "))
		}
		if strings.TrimSpace(text) == "" {
			return nil
		}
		return []sourceloc.Unit{{Text: text, Locator: loc}}
	}
}

func minerUMiddleText(raw json.RawMessage) string {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return text
	}
	var spans []struct {
		Type    string          `json:"type"`
		Content json.RawMessage `json:"content"`
	}
	if json.Unmarshal(raw, &spans) != nil {
		return ""
	}
	var out strings.Builder
	for _, span := range spans {
		out.WriteString(minerUMiddleText(span.Content))
	}
	return out.String()
}

func normalizedMinerUBox(box []float64) bool {
	if len(box) != 4 || box[0] >= box[2] || box[1] >= box[3] {
		return false
	}
	for _, v := range box {
		if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 || v > 1 {
			return false
		}
	}
	return true
}
