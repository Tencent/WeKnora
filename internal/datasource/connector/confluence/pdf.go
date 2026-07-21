package confluence

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"html"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"
	"github.com/go-rod/rod/lib/proto"

	"github.com/Tencent/WeKnora/internal/logger"
)

// ─────────────────────────────────────────────────────────────────────────────
// Main entry: fetch export_view HTML, inline images, render to PDF.
// ─────────────────────────────────────────────────────────────────────────────

// exportViewPageToPDF fetches a Confluence Cloud page's body.export_view HTML,
// inlines all embedded images as base64 data URIs, wraps the result in a
// styled HTML document, and renders it to PDF via headless Chrome.
//
// Returns the PDF bytes and a suggested filename.
func exportViewPageToPDF(ctx context.Context, client *Client, pageID, pageTitle string) ([]byte, string, error) {
	// ── Step 1: Fetch page with body.export_view ──────────────────────
	logger.Infof(ctx, "[Confluence] fetching export_view for page %s", pageID)

	exportHTML, err := fetchExportViewHTML(ctx, client, pageID)
	if err != nil {
		return nil, "", err
	}

	// ── Step 2: Fetch attachment list ─────────────────────────────────
	attachments, err := fetchAttachmentList(ctx, client, pageID)
	if err != nil {
		logger.Warnf(ctx, "[Confluence] failed to list attachments for page %s: %v", pageID, err)
		// Non-fatal: images may still render via src URLs.
	}

	attMap := make(map[string]string, len(attachments))
	for _, a := range attachments {
		attMap[a.Title] = a.ID
	}
	logger.Infof(ctx, "[Confluence] page %s: %d attachments", pageID, len(attachments))

	// ── Step 3: Clean Confluence storage-format junk & inline images ──
	exportHTML = cleanConfluenceHTML(exportHTML)
	exportHTML = inlineImages(ctx, client, pageID, attMap, exportHTML)

	// ── Step 4: Wrap in full HTML document ────────────────────────────
	fullHTML := wrapHTMLDocument(pageTitle, exportHTML)

	// ── Step 5: Render to PDF via go-rod ──────────────────────────────
	logger.Infof(ctx, "[Confluence] rendering PDF for page %s (%s)", pageID, pageTitle)
	pdfData, err := renderToPDF(ctx, fullHTML)
	if err != nil {
		return nil, "", fmt.Errorf("render PDF for page %s: %w", pageID, err)
	}

	filename := safeFilename(pageTitle) + ".pdf"
	logger.Infof(ctx, "[Confluence] exported page %s (%s) as PDF: %d bytes",
		pageID, pageTitle, len(pdfData))

	return pdfData, filename, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// API helpers
// ─────────────────────────────────────────────────────────────────────────────

// fetchExportViewHTML retrieves the body.export_view HTML for a page.
func fetchExportViewHTML(ctx context.Context, client *Client, pageID string) (string, error) {
	path := fmt.Sprintf("/rest/api/content/%s?expand=body.export_view,space", pageID)

	var pageResp confluencePageWithExportView
	_, err := client.doRequest(ctx, http.MethodGet, path, &pageResp)
	if err != nil {
		return "", fmt.Errorf("fetch page %s export_view: %w", pageID, err)
	}

	html := pageResp.Body.ExportView.Value
	if html == "" {
		return "", fmt.Errorf("page %s: empty body.export_view", pageID)
	}
	return html, nil
}

// fetchAttachmentList retrieves all attachments for a page, handling pagination.
func fetchAttachmentList(ctx context.Context, client *Client, pageID string) ([]confluenceAttachment, error) {
	var allAttachments []confluenceAttachment
	start := 0
	limit := 50

	for {
		path := fmt.Sprintf("/rest/api/content/%s/child/attachment?start=%d&limit=%d",
			pageID, start, limit)
		var resp confluenceAttachmentResponse
		_, err := client.doRequest(ctx, http.MethodGet, path, &resp)
		if err != nil {
			return nil, fmt.Errorf("fetch attachments for page %s (start=%d): %w", pageID, start, err)
		}

		allAttachments = append(allAttachments, resp.Results...)

		if len(resp.Results) < limit {
			break
		}
		start += len(resp.Results)
	}

	return allAttachments, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// HTML cleaning
// ─────────────────────────────────────────────────────────────────────────────

// Pre-compiled regexes for HTML cleaning.
var (
	reAcLayout             = regexp.MustCompile(`(?i)<ac:layout(?:\s[^>]*)?>`)
	reAcLayoutClose        = regexp.MustCompile(`(?i)</ac:layout>`)
	reAcLayoutSection      = regexp.MustCompile(`(?i)<ac:layout-section[^>]*>`)
	reAcLayoutSectionClose = regexp.MustCompile(`(?i)</ac:layout-section>`)
	reAcLayoutCellWidth    = regexp.MustCompile(`(?i)<ac:layout-cell\s+ac:width="(\d+)"[^>]*>`)
	reAcLayoutCell         = regexp.MustCompile(`(?i)<ac:layout-cell(?:\s[^>]*)?>`)
	reAcLayoutCellClose    = regexp.MustCompile(`(?i)</ac:layout-cell>`)
	reAcLayoutCellContent  = regexp.MustCompile(`(?i)<ac:layout-cell-content(?:\s[^>]*)?>`)
	reAcLayoutCellContentC = regexp.MustCompile(`(?i)</ac:layout-cell-content>`)
	reAcImageSelfClose     = regexp.MustCompile(`(?i)<ac:image[^>]*/>`)
	reAcImageOpen          = regexp.MustCompile(`(?i)<ac:image[^>]*>`)
	reAcImageClose         = regexp.MustCompile(`(?i)</ac:image>`)
	reRiAttachmentSelf     = regexp.MustCompile(`(?i)<ri:attachment[^>]*/>`)
	reRiAttachmentPair     = regexp.MustCompile(`(?i)<ri:attachment[^>]*>.*?</ri:attachment>`)
	reRiPageSelfClose      = regexp.MustCompile(`(?i)<ri:page[^>]*/>`)
	reImgAlign             = regexp.MustCompile(`(?i)(<img\s[^>]*?)\bac:align="([^"]*)"([^>]*>)`)
	reStyleAttr            = regexp.MustCompile(`(?i)\bstyle="([^"]*)"`)
	reAcAlignAttr          = regexp.MustCompile(`(?i)\sac:align="[^"]*"`)
	reAcLayoutFitAttr      = regexp.MustCompile(`(?i)\sac:layout-fit-to-page="[^"]*"`)
	reAcHeightAttr         = regexp.MustCompile(`(?i)\sac:height="[^"]*"`)
	reAcWidthAttr          = regexp.MustCompile(`(?i)\sac:width="[^"]*"`)
	reDataMceSrc           = regexp.MustCompile(`(?i)\sdata-mce-src="[^"]*"`)

	// Image inlining regexes.
	reImgTag     = regexp.MustCompile(`(?i)<img\s[^>]*>`)
	reSrcAttr    = regexp.MustCompile(`(?i)\bsrc\s*=\s*"([^"]*)"`)
	reAltAttr    = regexp.MustCompile(`(?i)\balt\s*=\s*"([^"]*)"`)
	reResourceID = regexp.MustCompile(`(?i)\bdata-linked-resource-id\s*=\s*"([^"]*)"`)
)

// cleanConfluenceHTML converts Confluence storage-format elements into
// valid HTML so the browser PDF renderer can display them correctly.
//
// Mapping:
//
//	ac:layout              → <table>
//	ac:layout-section      → <tr>
//	ac:layout-cell         → <td>  (ac:width → style width)
//	ac:layout-cell-content → <div>
//	ac:image               → unwrapped (children kept)
//	ri:attachment / ri:page→ removed (metadata only)
//	ac:align on <img>      → inline CSS alignment
//	data-mce-src           → removed (stale duplicate of src)
func cleanConfluenceHTML(html string) string {
	// ── Layout → table ────────────────────────────────────────────
	html = reAcLayout.ReplaceAllString(html, `<table style="width:100%;border-collapse:collapse;border:none;">`)
	html = reAcLayoutClose.ReplaceAllString(html, `</table>`)

	html = reAcLayoutSection.ReplaceAllString(html, `<tr>`)
	html = reAcLayoutSectionClose.ReplaceAllString(html, `</tr>`)

	html = reAcLayoutCellWidth.ReplaceAllString(html,
		`<td style="vertical-align:top;width:${1}px;border:none;padding:4px;">`)
	html = reAcLayoutCell.ReplaceAllString(html, `<td style="vertical-align:top;border:none;padding:4px;">`)
	html = reAcLayoutCellClose.ReplaceAllString(html, `</td>`)

	html = reAcLayoutCellContent.ReplaceAllString(html, `<div>`)
	html = reAcLayoutCellContentC.ReplaceAllString(html, `</div>`)

	// ── ac:image → unwrap (keep children) ─────────────────────────
	html = reAcImageSelfClose.ReplaceAllString(html, "")
	html = reAcImageOpen.ReplaceAllString(html, "")
	html = reAcImageClose.ReplaceAllString(html, "")

	// ── ri:attachment / ri:page → remove (metadata) ───────────────
	html = reRiAttachmentSelf.ReplaceAllString(html, "")
	html = reRiAttachmentPair.ReplaceAllString(html, "")
	html = reRiPageSelfClose.ReplaceAllString(html, "")

	// ── <img> ac:align → CSS alignment ────────────────────────────
	html = reImgAlign.ReplaceAllStringFunc(html, func(tag string) string {
		m := reImgAlign.FindStringSubmatch(tag)
		if m == nil {
			return tag
		}
		prefix, align, suffix := m[1], strings.ToLower(m[2]), m[3]
		var css string
		switch align {
		case "center":
			css = "display:block;margin-left:auto;margin-right:auto;"
		case "right":
			css = "float:right;"
		case "left":
			css = "float:left;"
		}
		if css != "" {
			if sm := reStyleAttr.FindStringSubmatch(prefix + suffix); sm != nil {
				old := sm[0]
				newAttr := fmt.Sprintf(`style="%s%s"`, sm[1], css)
				tag = strings.Replace(tag, old, newAttr, 1)
			} else {
				tag = fmt.Sprintf(`%s style="%s"%s`, prefix, css, suffix)
			}
		}
		tag = reAcAlignAttr.ReplaceAllString(tag, "")
		return tag
	})

	// Remove other ac:* attributes from tags.
	html = reAcLayoutFitAttr.ReplaceAllString(html, "")
	html = reAcHeightAttr.ReplaceAllString(html, "")
	html = reAcWidthAttr.ReplaceAllString(html, "")
	html = reDataMceSrc.ReplaceAllString(html, "")

	return html
}

// ─────────────────────────────────────────────────────────────────────────────
// Image inlining
// ─────────────────────────────────────────────────────────────────────────────

// inlineImages finds all <img> tags and replaces each src with a base64
// data URI by downloading via the attachment download API.
//
// Attachment ID resolution order:
//  1. data-linked-resource-id attribute (most reliable, direct ID)
//  2. filename from src URL → matched against attachment list
func inlineImages(ctx context.Context, client *Client, pageID string,
	attMap map[string]string, html string,
) string {
	return reImgTag.ReplaceAllStringFunc(html, func(imgTag string) string {
		srcMatch := reSrcAttr.FindStringSubmatch(imgTag)
		if srcMatch == nil {
			return imgTag
		}
		src := srcMatch[1]

		// Skip already-inlined data URIs.
		if strings.HasPrefix(src, "data:") {
			return imgTag
		}

		// ── Method 1: data-linked-resource-id ─────────────────────
		var attID string
		if m := reResourceID.FindStringSubmatch(imgTag); m != nil {
			attID = m[1]
		}

		// ── Method 2: filename lookup ─────────────────────────────
		var filename string
		if attID == "" {
			filename = extractFilenameFromURL(src)
			if filename == "" {
				filename = extractAltFromTag(imgTag)
			}
			if filename != "" {
				attID = attMap[filename]
			}
		}

		if attID == "" {
			logger.Warnf(ctx, "[Confluence] cannot resolve attachment for src=%q, skipping",
				truncate(src, 80))
			return imgTag
		}

		// Download via attachment API.
		downloadPath := fmt.Sprintf("/rest/api/content/%s/child/attachment/%s/download?os_authType=basic",
			pageID, attID)

		label := filename
		if label == "" {
			label = "attachment-" + attID
		}
		logger.Infof(ctx, "[Confluence] downloading %s (att=%s)", label, attID)

		imgBytes, contentType, err := client.doRequestRaw(ctx, downloadPath)
		if err != nil {
			logger.Warnf(ctx, "[Confluence] download failed for %s: %v", label, err)
			return imgTag
		}

		if contentType == "" {
			contentType = "image/png"
		}
		contentType = strings.Split(contentType, ";")[0]
		contentType = strings.TrimSpace(contentType)

		b64 := base64.StdEncoding.EncodeToString(imgBytes)
		dataURI := fmt.Sprintf("data:%s;base64,%s", contentType, b64)

		newTag := strings.Replace(imgTag, src, dataURI, 1)
		newTag = reDataMceSrc.ReplaceAllString(newTag, "")
		logger.Infof(ctx, "[Confluence] inlined %s: %d bytes -> %s", label, len(imgBytes), contentType)
		return newTag
	})
}

// extractFilenameFromURL extracts the filename from a URL like
// "https://xxx/wiki/download/attachments/123/image.png?api=v2"
func extractFilenameFromURL(rawURL string) string {
	if idx := strings.Index(rawURL, "?"); idx >= 0 {
		rawURL = rawURL[:idx]
	}
	if idx := strings.LastIndex(rawURL, "/"); idx >= 0 {
		return rawURL[idx+1:]
	}
	return rawURL
}

// extractAltFromTag extracts the alt attribute value from an <img> tag.
func extractAltFromTag(imgTag string) string {
	m := reAltAttr.FindStringSubmatch(imgTag)
	if m != nil {
		return m[1]
	}
	return ""
}

// ─────────────────────────────────────────────────────────────────────────────
// HTML document wrapper
// ─────────────────────────────────────────────────────────────────────────────

// wrapHTMLDocument wraps an HTML body fragment in a full document with CSS.
// The title is escaped to prevent HTML injection.
func wrapHTMLDocument(title, bodyHTML string) string {
	safeTitle := html.EscapeString(title)
	return fmt.Sprintf(`<!DOCTYPE html>
<html>
<head>
<meta charset="utf-8">
<title>%s</title>
<style>
  @page { size: A4; margin: 20mm; }
  html { font-size: 14px; }
  body { font-family: -apple-system, "Segoe UI", Helvetica, Arial, sans-serif; line-height: 1.6; color: #333; }
  h1 { font-size: 1.8em; margin-bottom: 0.5em; border-bottom: 1px solid #ccc; padding-bottom: 0.3em; }
  h2 { font-size: 1.4em; }
  h3 { font-size: 1.2em; }
  img { max-width: 100%% !important; height: auto !important; width: auto !important; }
  table { border-collapse: collapse; width: 100%%; }
  td, th { border: 1px solid #ccc; padding: 6px 10px; text-align: left; }
  th { background: #f5f5f5; font-weight: 600; }
  pre, code { background: #f5f5f5; padding: 2px 4px; border-radius: 3px; font-size: 0.9em; }
  pre { padding: 12px; overflow-x: auto; }
  blockquote { border-left: 3px solid #ccc; margin-left: 0; padding-left: 1em; color: #555; }
</style>
</head>
<body>
<h1>%s</h1>
%s
</body>
</html>`, safeTitle, safeTitle, bodyHTML)
}

// ─────────────────────────────────────────────────────────────────────────────
// PDF rendering via go-rod (auto-downloads browser + fonts if needed)
// ─────────────────────────────────────────────────────────────────────────────

// hasSystemCJKFonts checks whether the system already has CJK font coverage.
func hasSystemCJKFonts() bool {
	out, err := exec.Command("fc-list", ":lang=zh").Output()
	return err == nil && len(bytes.TrimSpace(out)) > 0
}

// ensureCJKFonts downloads a CJK font to the user's local font directory if
// no system CJK fonts are found. This ensures Confluence pages with Chinese,
// Japanese, or Korean content render correctly in the generated PDF.
// The font is cached in ~/.local/share/fonts/ and only downloaded once.
func ensureCJKFonts(ctx context.Context) {
	if hasSystemCJKFonts() {
		return
	}

	home, err := os.UserHomeDir()
	if err != nil {
		logger.Warnf(ctx, "[Confluence] cannot determine home dir for CJK font: %v", err)
		return
	}

	fontDir := filepath.Join(home, ".local", "share", "fonts")
	fontPath := filepath.Join(fontDir, "NotoSansSC.ttf")

	if _, err := os.Stat(fontPath); err == nil {
		return // already cached
	}

	logger.Infof(ctx, "[Confluence] no CJK fonts found, downloading Noto Sans SC...")

	const fontURL = "https://cdn.jsdelivr.net/gh/notofonts/noto-cjk@main/Sans/Variable/TTF/NotoSansCJKsc-VF.ttf"

	dlCtx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(dlCtx, http.MethodGet, fontURL, nil)
	if err != nil {
		logger.Warnf(ctx, "[Confluence] CJK font download request failed: %v", err)
		return
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		logger.Warnf(ctx, "[Confluence] CJK font download failed: %v", err)
		return
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		logger.Warnf(ctx, "[Confluence] CJK font download: HTTP %d", resp.StatusCode)
		return
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil || len(data) < 1000 {
		logger.Warnf(ctx, "[Confluence] CJK font download: invalid response")
		return
	}

	if err := os.MkdirAll(fontDir, 0o755); err != nil {
		logger.Warnf(ctx, "[Confluence] cannot create font dir: %v", err)
		return
	}
	if err := os.WriteFile(fontPath, data, 0o644); err != nil {
		logger.Warnf(ctx, "[Confluence] cannot write CJK font: %v", err)
		return
	}

	// Refresh fontconfig cache (best-effort; Chromium also scans the dir directly)
	_ = exec.Command("fc-cache", "-f", fontDir).Run()

	logger.Infof(ctx, "[Confluence] CJK font installed: %s (%d bytes)", fontPath, len(data))
}

// renderPDFTimeout is the maximum time allowed for a single page PDF render.
const renderPDFTimeout = 60 * time.Second

// renderToPDF renders an HTML string to PDF using headless Chrome.
// Uses go-rod which automatically downloads a browser binary if none is found,
// requiring zero manual installation from the user.
func renderToPDF(ctx context.Context, htmlContent string) ([]byte, error) {
	// Enforce a per-page timeout so a stuck render cannot block the entire sync.
	ctx, cancel := context.WithTimeout(ctx, renderPDFTimeout)
	defer cancel()

	// Ensure CJK fonts are available before rendering
	ensureCJKFonts(ctx)

	tmpFile, err := os.CreateTemp("", "confluence-*.html")
	if err != nil {
		return nil, fmt.Errorf("create temp file: %w", err)
	}
	defer func() { _ = os.Remove(tmpFile.Name()) }()

	if _, err := tmpFile.WriteString(htmlContent); err != nil {
		_ = tmpFile.Close()
		return nil, fmt.Errorf("write temp HTML: %w", err)
	}
	_ = tmpFile.Close()

	fileURL := "file://" + tmpFile.Name()

	// launcher.NewBrowser() auto-downloads Chromium to ~/.cache/rod/browser/
	// if no system browser is found. This makes the feature work out-of-the-box
	// on any platform without manual Chrome installation.
	browserPath, err := launcher.NewBrowser().Get()
	if err != nil {
		return nil, fmt.Errorf("locate/download browser: %w", err)
	}

	l := launcher.New().
		Bin(browserPath).
		Headless(true).
		NoSandbox(true).
		Set("disable-gpu")

	controlURL, err := l.Launch()
	if err != nil {
		return nil, fmt.Errorf("launch browser: %w", err)
	}

	browser := rod.New().ControlURL(controlURL)
	if err := browser.Connect(); err != nil {
		return nil, fmt.Errorf("connect to browser: %w", err)
	}
	defer func() { _ = browser.Close() }()

	page, err := browser.Page(proto.TargetCreateTarget{URL: fileURL})
	if err != nil {
		return nil, fmt.Errorf("create page: %w", err)
	}

	// Wait for body to be ready (respects context timeout)
	if err := page.Context(ctx).WaitElementsMoreThan("body", 0); err != nil {
		return nil, fmt.Errorf("wait for body: %w", err)
	}

	// Small delay to ensure rendering is complete
	select {
	case <-ctx.Done():
		return nil, fmt.Errorf("render cancelled: %w", ctx.Err())
	case <-time.After(1 * time.Second):
	}

	// Print to PDF (A4 size)
	paperW := 8.27  // A4 width in inches
	paperH := 11.69 // A4 height in inches
	margin := 0.787 // 20mm in inches
	reader, err := page.Context(ctx).PDF(&proto.PagePrintToPDF{
		PaperWidth:      &paperW,
		PaperHeight:     &paperH,
		PrintBackground: true,
		MarginTop:       &margin,
		MarginBottom:    &margin,
		MarginLeft:      &margin,
		MarginRight:     &margin,
	})
	if err != nil {
		return nil, fmt.Errorf("print to PDF: %w", err)
	}
	defer func() { _ = reader.Close() }()

	pdfData, err := io.ReadAll(reader)
	if err != nil {
		return nil, fmt.Errorf("read PDF stream: %w", err)
	}

	return pdfData, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// Internal helpers
// ─────────────────────────────────────────────────────────────────────────────
