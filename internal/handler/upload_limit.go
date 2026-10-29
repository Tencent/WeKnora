package handler

import (
	stderrors "errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// uploadEnvelopeSlack is the multipart framing allowed on top of the file
// itself, so a body cap refuses a genuinely oversized upload without
// rejecting a legal one for its boundary lines.
const uploadEnvelopeSlack = 1 << 20

// skillSourceJSONMaxBytes is the cap on {"source":"..."} (and similar) JSON
// bodies for skill endpoints. The zip is fetched server-side under
// GetMaxSkillBundleSize; this only needs to hold a locator URL.
const skillSourceJSONMaxBytes = 64 << 10

// knowledgeBaseJSONMaxBytes caps a knowledge-base definition body.
//
// A definition carries scalars, model ids and prompt instructions, whose
// per-field limits are already enforced by ValidateKnowledgeBasePromptInstructions;
// the largest legal body is a few hundred KiB. Without a cap the handler
// buffered whatever arrived: the Logger middleware only retains 10 KiB, but it
// still reads up to 10 KiB+1 and then hands the rest of the stream to this
// handler, so an authenticated POST could make one request occupy as much
// memory as the process will surrender.
const knowledgeBaseJSONMaxBytes = 1 << 20

// hybridSearchJSONMaxBytes caps a hybrid-search body.
//
// The body is a query, filters and an optional precomputed `query_embedding`.
// That vector is the only large field: an 8192-dimension float32 array is about
// 160 KiB of JSON, so 1 MiB leaves room for the widest embedding model plus a
// generous filter list while still bounding what one request can allocate.
// HybridSearch is reachable by any Viewer of the knowledge base, so the cap
// applies to a much broader audience than the knowledge-base lifecycle routes,
// which need manage_kbs.
const hybridSearchJSONMaxBytes = 1 << 20

// embedChatJSONMaxBytes caps a visitor's embed chat body before the channel's
// constraints are merged into it.
//
// The embed chat routes do not hand the visitor's body to the QA handler: they
// read it whole, rewrite it as JSON and hand on the rewrite. The QA handler
// caps its own read, but by then that read sees the rewrite, so the visitor's
// bytes were already buffered here. That matters more than the authenticated
// chat routes because the embed publish token is public and embed visitors have
// no account at all: one anonymous request could set the process's memory
// high-water mark.
//
// The largest legal body is the one the delegated handler accepts, which is
// bounded by the inline attachment budget (five uploads, 100 MiB decoded, about
// 134 MiB once base64-encoded). The cap is that encoded maximum plus 2 MiB of
// JSON envelope, so a request the handler would accept is never refused here
// and the per-attachment checks stay the authority on what may be decoded.
const embedChatJSONMaxBytes = 100*1024*1024*4/3 + 2*1024*1024

// limitUploadBody caps the request body before anything parses it.
//
// It has to run before FormFile rather than after: ParseMultipartForm buffers
// the whole request, spilling to temp files, so a handler that only checks the
// declared part size has already accepted every byte by the time it looks.
// nginx still enforces MAX_FILE_SIZE on location /api/; this is the same cap
// for requests that reach the app without that proxy.
func limitUploadBody(c *gin.Context, maxBytes int64) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBytes+uploadEnvelopeSlack)
}

// limitJSONBody caps a JSON body with no multipart slack.
func limitJSONBody(c *gin.Context, maxBytes int64) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBytes)
}

// limitSkillUploadBody uses the zip cap for multipart skill uploads and a
// small JSON cap for source locators. Applying the zip cap to JSON would let
// a {"source":"..."} POST occupy hundreds of megabytes.
func limitSkillUploadBody(c *gin.Context, zipMaxBytes int64) {
	if strings.HasPrefix(c.ContentType(), "application/json") {
		limitJSONBody(c, skillSourceJSONMaxBytes)
		return
	}
	limitUploadBody(c, zipMaxBytes)
}

// isRequestBodyTooLarge reports whether a multipart parsing error is the body
// cap firing rather than a malformed or absent part.
//
// The two need different answers. An oversized upload is a limit the caller
// can see and act on, while a missing field is a different request; a handler
// that treats the first as the second reports "no file was sent" for a file
// that was sent and rejected.
func isRequestBodyTooLarge(err error) bool {
	var tooLarge *http.MaxBytesError
	return stderrors.As(err, &tooLarge)
}
