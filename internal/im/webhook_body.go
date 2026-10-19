package im

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// maxWebhookBodyBytes caps an inbound IM callback body.
//
// The callback endpoints are public: a platform cannot present a bearer token,
// so every request reaches the adapters before any credential is checked. Each
// platform's signature is computed over the body itself, which means the bytes
// have to be read before the request can be authenticated at all. An unbounded
// read therefore lets one unauthenticated POST ask the process to buffer as much
// memory as it is willing to surrender.
//
// 4 MiB sits far above any real callback - chat messages, interactive cards and
// block payloads are kilobytes, and attachments travel as URLs the server
// fetches separately under maxIMAttachmentBytes - while still bounding what a
// single request can allocate.
const maxWebhookBodyBytes = 4 << 20

// LimitWebhookBody caps the callback body before an adapter reads it, matching
// how internal/handler caps uploads and JSON bodies on the authenticated
// routes.
//
// Call it ahead of any read of c.Request.Body. Calling it more than once per
// request is harmless: adapters that buffer the bytes replace c.Request.Body
// with an in-memory copy, so a later cap simply wraps that copy.
func LimitWebhookBody(c *gin.Context) {
	if c.Request == nil || c.Request.Body == nil {
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxWebhookBodyBytes)
}
