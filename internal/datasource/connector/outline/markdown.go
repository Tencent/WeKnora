package outline

import (
	"context"
	"encoding/base64"
	"net/http"
	"regexp"
	"strings"

	"github.com/Tencent/WeKnora/internal/logger"
)

const (
	// maxInlineImages mirrors ImageResolver.maxRemoteImages: ingestion processes
	// at most 30 data URIs per document, so inlining more would grow the upload
	// without ever producing an image.
	maxInlineImages = 30

	// maxImageBytes stays below ImageResolver.maxRemoteImageSize (10MB), which
	// is applied to the decoded bytes. Anything larger is left as a URL rather
	// than being downscaled — see the package doc's known limitations.
	maxImageBytes = 9 * 1024 * 1024

	// maxDocInlineBytes caps the data URIs one document may carry, matching the
	// Confluence connector's per-page budget. Without it maxInlineImages images
	// at maxImageBytes would put ~360MB of base64 into a single upload.
	maxDocInlineBytes = 50 << 20
)

// attachmentDownloader is the slice of the API client that image inlining needs.
// Narrowing it keeps the markdown transformation testable without an HTTP server.
type attachmentDownloader interface {
	DownloadAttachment(ctx context.Context, attachmentID string) ([]byte, string, error)
}

// attachmentImageRe matches a Markdown image whose target is an Outline
// attachment, in either the relative form Outline writes
//
//	![alt](/api/attachments.redirect?id=<uuid> "title")
//
// or the absolute form that appears in documents copied between instances.
// Group 1 is the alt text, group 2 the attachment id, group 3 the remainder
// inside the parens (query tail and/or a quoted title).
var attachmentImageRe = regexp.MustCompile(
	`!\[([^\]]*)\]\(\s*(?:https?://[^/\s)]+)?/api/attachments\.redirect\?id=([a-zA-Z0-9-]+)([^)]*)\)`,
)

// linkTitleRe extracts the optional quoted title from an image target tail.
var linkTitleRe = regexp.MustCompile(`"([^"]*)"`)

// embedAttachmentImages rewrites every Outline attachment image into an inline
// base64 data URI, returning the new Markdown and how many images were inlined.
//
// Why inline rather than pass the URL through: ImageResolver.ResolveRemoteImages
// downloads http(s) images anonymously, and Outline attachment URLs require the
// Bearer token, so a passed-through URL 401s and the image disappears. A data
// URI is picked up by ResolveDataURIImages, stored, and rewritten to
// resource://<handle> while staying inside the parent document.
//
// An image that cannot be inlined (download failure, oversize, non-image, past
// the cap or the byte budget) keeps its original link: the text around it is
// still worth ingesting, and a broken image is easier to diagnose than a
// silently deleted one.
//
// Each attachment is downloaded once per document; a repeated image reuses the
// result but still counts against both limits per occurrence, because ingestion
// keeps every data URI occurrence.
func embedAttachmentImages(ctx context.Context, cli attachmentDownloader, md string) (string, int) {
	matches := attachmentImageRe.FindAllStringSubmatchIndex(md, -1)
	if len(matches) == 0 {
		return md, 0
	}

	var out strings.Builder
	inlined := 0
	inlineBytes := 0
	last := 0
	// dataURIs caches each attachment's data URI; "" records one that cannot be
	// inlined, so a failed download is not retried for every occurrence.
	dataURIs := make(map[string]string)

	for _, m := range matches {
		whole := md[m[0]:m[1]]
		alt := md[m[2]:m[3]]
		attachmentID := md[m[4]:m[5]]
		tail := md[m[6]:m[7]]

		out.WriteString(md[last:m[0]])
		last = m[1]

		if inlined >= maxInlineImages {
			logger.Warnf(ctx, "[Outline] attachment %s not inlined: document already has %d images (ingestion cap)",
				attachmentID, maxInlineImages)
			out.WriteString(whole)
			continue
		}

		dataURI, seen := dataURIs[attachmentID]
		if !seen {
			dataURI = attachmentDataURI(ctx, cli, attachmentID)
			dataURIs[attachmentID] = dataURI
		}
		if dataURI == "" {
			out.WriteString(whole)
			continue
		}
		if inlineBytes+len(dataURI) > maxDocInlineBytes {
			logger.Warnf(ctx, "[Outline] attachment %s not inlined: document already carries %d bytes of images",
				attachmentID, inlineBytes)
			out.WriteString(whole)
			continue
		}

		// The alt text is the only description that reaches embeddings, so fall
		// back to Outline's link title when the author left the alt empty.
		altText := strings.TrimSpace(alt)
		if altText == "" {
			if t := linkTitleRe.FindStringSubmatch(tail); len(t) == 2 {
				altText = strings.TrimSpace(t[1])
			}
		}
		altText = sanitizeAltText(altText)

		// Emit exactly "![alt](data:<mime>;base64,<payload>)" with nothing after
		// the payload: WeKnora's data-URI regex captures everything up to the
		// closing paren, so a trailing title would be folded into the base64
		// payload and the image would be dropped on decode.
		out.WriteString("![")
		out.WriteString(altText)
		out.WriteString("](")
		out.WriteString(dataURI)
		out.WriteString(")")
		inlined++
		inlineBytes += len(dataURI)
	}

	out.WriteString(md[last:])
	return out.String(), inlined
}

// attachmentDataURI downloads one attachment and returns it as
// "data:<mime>;base64,<payload>", or "" when it cannot be inlined (download
// failure, oversize, not an image). The reason is logged here.
func attachmentDataURI(ctx context.Context, cli attachmentDownloader, attachmentID string) string {
	data, contentType, err := cli.DownloadAttachment(ctx, attachmentID)
	if err != nil {
		logger.Warnf(ctx, "[Outline] attachment %s download failed, keeping original link: %v",
			attachmentID, err)
		return ""
	}
	if len(data) > maxImageBytes {
		logger.Warnf(ctx,
			"[Outline] attachment %s is over the %d byte inline limit; keeping original link",
			attachmentID, maxImageBytes)
		return ""
	}
	mime := imageMIME(contentType, data)
	if mime == "" {
		logger.Infof(ctx, "[Outline] attachment %s is not an image (content-type %q); keeping original link",
			attachmentID, contentType)
		return ""
	}
	return "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data)
}

// imageMIME decides the data URI media type, preferring the server's
// Content-Type (parameters stripped) and falling back to sniffing the bytes.
// Returns "" when the attachment is not an image, which keeps PDFs and other
// file attachments from being inlined as broken images.
func imageMIME(contentType string, data []byte) string {
	ct := strings.ToLower(strings.TrimSpace(strings.Split(contentType, ";")[0]))
	if strings.HasPrefix(ct, "image/") {
		return ct
	}
	sniffed := strings.ToLower(strings.TrimSpace(strings.Split(http.DetectContentType(data), ";")[0]))
	if strings.HasPrefix(sniffed, "image/") {
		return sniffed
	}
	return ""
}

// sanitizeAltText keeps the alt text on one line and free of the brackets that
// would break the image syntax.
func sanitizeAltText(s string) string {
	s = strings.NewReplacer("\r", " ", "\n", " ", "[", "(", "]", ")").Replace(s)
	return strings.TrimSpace(s)
}

// escapeArtifactLine matches a line whose entire content is Outline's
// serialization noise for an empty paragraph or hard break: a run of bare
// backslashes and literal "\n" tokens, and nothing else.
//
// Measured on a 1123-document instance: 687 lines holding a single backslash
// and 1463 literal "\n" tokens, spread over 13 of 15 collections. Rendered
// verbatim they become visible garbage inside chunks. Requiring the WHOLE line
// to match is what keeps legitimate inline escapes (\[, \], \*) untouched.
var escapeArtifactLine = regexp.MustCompile(`^(?:\\n|\\)+$`)

// fenceLine matches an opening or closing fenced-code delimiter.
var fenceLine = regexp.MustCompile("^\\s*(?:```|~~~)")

// stripEscapeArtifacts removes Outline's empty-paragraph escape artifacts,
// leaving the content of fenced code blocks alone — a code sample may legitimately
// contain a bare backslash line.
func stripEscapeArtifacts(md string) string {
	if !strings.Contains(md, "\\") {
		return md
	}
	lines := strings.Split(md, "\n")
	kept := make([]string, 0, len(lines))
	inFence := false
	for _, line := range lines {
		if fenceLine.MatchString(line) {
			inFence = !inFence
			kept = append(kept, line)
			continue
		}
		if !inFence && escapeArtifactLine.MatchString(strings.TrimSpace(line)) {
			continue
		}
		kept = append(kept, line)
	}
	return strings.Join(kept, "\n")
}
