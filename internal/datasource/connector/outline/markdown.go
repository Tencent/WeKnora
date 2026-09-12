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
// the cap) keeps its original link: the text around it is still worth ingesting,
// and a broken image is easier to diagnose than a silently deleted one.
func embedAttachmentImages(ctx context.Context, cli attachmentDownloader, md string) (string, int) {
	matches := attachmentImageRe.FindAllStringSubmatchIndex(md, -1)
	if len(matches) == 0 {
		return md, 0
	}

	var out strings.Builder
	inlined := 0
	last := 0

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

		data, contentType, err := cli.DownloadAttachment(ctx, attachmentID)
		if err != nil {
			logger.Warnf(ctx, "[Outline] attachment %s download failed, keeping original link: %v",
				attachmentID, err)
			out.WriteString(whole)
			continue
		}
		if len(data) > maxImageBytes {
			logger.Warnf(ctx, "[Outline] attachment %s is %d bytes, over the %d byte inline limit; keeping original link",
				attachmentID, len(data), maxImageBytes)
			out.WriteString(whole)
			continue
		}

		mime := imageMIME(contentType, data)
		if mime == "" {
			logger.Infof(ctx, "[Outline] attachment %s is not an image (content-type %q); keeping original link",
				attachmentID, contentType)
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
		out.WriteString("](data:")
		out.WriteString(mime)
		out.WriteString(";base64,")
		out.WriteString(base64.StdEncoding.EncodeToString(data))
		out.WriteString(")")
		inlined++
	}

	out.WriteString(md[last:])
	return out.String(), inlined
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
