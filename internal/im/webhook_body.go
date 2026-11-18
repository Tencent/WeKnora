package im

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
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

// maxIMEncryptedAttachmentBytes is one AES block above maxIMAttachmentBytes.
// Attachments travel as AES-encrypted downloads and PKCS#7 padding can add a
// whole block, so a file at the attachment limit arrives slightly larger than
// the plaintext cap.
const maxIMEncryptedAttachmentBytes = maxIMAttachmentBytes + 16

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

// errResponseTooLarge reports that an outbound IM platform response exceeded
// its configured cap and was refused instead of buffered.
var errResponseTooLarge = fmt.Errorf("IM response body too large")

// ReadIMResponse reads an outbound IM platform response body, refusing anything
// larger than maxWebhookBodyBytes instead of buffering it.
//
// IM platforms are external services: a misconfigured or hostile platform can
// return an arbitrarily large body and WeKnora would hold all of it in memory.
// The same cap as LimitWebhookBody applies, so both directions of an IM
// integration are bounded by the same figure.
//
// A body that does not fit returns errResponseTooLarge rather than a shortened
// slice. Every caller either decodes the bytes as JSON or prints them in an
// error message, and a silently truncated body would decode as a smaller but
// still valid reply.
func ReadIMResponse(body io.Reader) ([]byte, error) {
	return readIMResponse(body, maxWebhookBodyBytes)
}

// ReadIMAttachmentResponse reads an outbound attachment download, refusing
// anything larger than maxIMEncryptedAttachmentBytes.
//
// Attachment downloads are the one response that legitimately carries a file
// larger than a callback body. Applying maxWebhookBodyBytes here would reject
// attachments between 4 MiB and maxIMAttachmentBytes that the rest of the IM
// pipeline accepts.
func ReadIMAttachmentResponse(body io.Reader) ([]byte, error) {
	return readIMResponse(body, maxIMEncryptedAttachmentBytes)
}

func readIMResponse(body io.Reader, limit int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("%w: limit is %d bytes", errResponseTooLarge, limit)
	}
	return data, nil
}

// DecodeIMResponse bounds an outbound IM platform response and decodes it into
// target, behaving exactly like json.NewDecoder(body).Decode(target) for every
// body that fits under the cap.
//
// Decoding needs the same bound as reading: a decoder stops once one JSON value
// is complete, but that value can itself be a multi-megabyte string, base64
// image or array, and the decoder allocates it in full before returning. The
// bytes are therefore read through ReadIMResponse first and decoded from
// memory, so an oversized reply fails with errResponseTooLarge instead of being
// partially decoded.
func DecodeIMResponse(body io.Reader, target any) error {
	data, err := ReadIMResponse(body)
	if err != nil {
		return err
	}
	return json.NewDecoder(bytes.NewReader(data)).Decode(target)
}
