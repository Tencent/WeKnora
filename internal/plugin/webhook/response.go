package webhook

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// callbackCSP sandboxes whatever a plugin answers a callback with: a
// document gets an opaque origin of its own and may run no script, submit
// no form and load nothing.
const callbackCSP = "sandbox; default-src 'none'; frame-ancestors 'none'"

// WriteResponse writes a plugin's answer to an inbound callback (a webhook,
// an IM platform's event). The answer is served on WeKnora's origin with a
// type the plugin chose, so a browser sent to the callback URL would run a
// script in it with the signed-in user's session; the headers keep it inert
// there. Machine callers, the ones callbacks are for, ignore them.
func WriteResponse(c *gin.Context, status int, contentType string, body []byte) {
	if status < 100 || status > 599 {
		status = http.StatusOK
	}
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	h := c.Writer.Header()
	h.Set("Content-Security-Policy", callbackCSP)
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("X-Frame-Options", "DENY")
	h.Set("Referrer-Policy", "no-referrer")
	c.Data(status, contentType, body)
}
