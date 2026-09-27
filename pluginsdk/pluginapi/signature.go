package pluginapi

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// MaxClockSkew bounds how old a signed request may be.
const MaxClockSkew = 5 * time.Minute

// Sign returns the signature of a request: hex HMAC-SHA256 over
// "<timestamp>\n<METHOD>\n<path>\n<body>". The path is the one the request is
// sent to, so a signed call cannot be replayed to another endpoint, or
// through a gateway to another plugin.
func Sign(secret []byte, timestamp int64, method, path string, body []byte) string {
	mac := hmac.New(sha256.New, secret)
	_, _ = fmt.Fprintf(mac, "%d\n%s\n%s\n", timestamp, method, path)
	_, _ = mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

// SignRequest sets the signature headers of a request with its body.
func SignRequest(req *http.Request, secret []byte, body []byte) {
	ts := time.Now().Unix()
	path := req.URL.EscapedPath()
	req.Header.Set(TimestampHeader, strconv.FormatInt(ts, 10))
	req.Header.Set(SignedPathHeader, path)
	req.Header.Set(SignatureHeader, Sign(secret, ts, req.Method, path, body))
}

// checkTimestamp reads a request timestamp and checks it against the clock.
func checkTimestamp(timestamp string, now time.Time) (int64, error) {
	ts, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("bad %s", TimestampHeader)
	}
	if d := now.Sub(time.Unix(ts, 0)); d > MaxClockSkew || d < -MaxClockSkew {
		return 0, fmt.Errorf("request timestamp is outside the allowed clock skew")
	}
	return ts, nil
}

// checkSignedPath accepts the path a request was signed for when the path
// it arrived at is that path or ends it: a proxy in front of a remote plugin
// may strip a prefix, nothing may change the rest.
func checkSignedPath(signed, received string) error {
	if signed == "" || !strings.HasPrefix(received, "/") || !strings.HasSuffix(signed, received) {
		return fmt.Errorf("request was signed for another path")
	}
	return nil
}

// VerifySignature checks a signed request against the secret and clock:
// method is the request's, signedPath the X-WeKnora-Signed-Path header and
// path the one the request arrived at.
func VerifySignature(
	secret []byte, timestamp, signature, method, signedPath, path string, body []byte, now time.Time,
) error {
	ts, err := checkTimestamp(timestamp, now)
	if err != nil {
		return err
	}
	if err := checkSignedPath(signedPath, path); err != nil {
		return err
	}
	want := Sign(secret, ts, method, signedPath, body)
	if !hmac.Equal([]byte(want), []byte(signature)) {
		return fmt.Errorf("bad signature")
	}
	return nil
}

// BodyBudget bounds the request bodies held in memory at once while their
// signatures are checked, so many large requests (which anyone reaching a
// remote plugin can send) cannot exhaust its memory.
type BodyBudget struct {
	mu    sync.Mutex
	free  int64
	limit int64
}

// NewBodyBudget holds up to limit bytes of request bodies at once.
func NewBodyBudget(limit int64) *BodyBudget { return &BodyBudget{free: limit, limit: limit} }

func (b *BodyBudget) take(n int64) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if n > b.free {
		return false
	}
	b.free -= n
	return true
}

func (b *BodyBudget) give(n int64) {
	b.mu.Lock()
	b.free += n
	b.mu.Unlock()
}

// ReadSigned reads and verifies a signed request's body, up to limit bytes,
// within budget. It turns away what it can before reading: a request
// without a well-formed, current signature, one declaring a body over limit,
// and one the budget cannot hold now (retryable). On success the request's
// body is replaced by the bytes read, which count against the budget until
// release is called, once the request is handled.
func ReadSigned(
	r *http.Request, secret []byte, limit int64, budget *BodyBudget, now time.Time,
) (body []byte, release func(), perr *Error) {
	release = func() {}
	if _, err := checkTimestamp(r.Header.Get(TimestampHeader), now); err != nil {
		return nil, release, Errorf(CodeUnauthorized, "%v", err)
	}
	if sig := r.Header.Get(SignatureHeader); len(sig) != sha256.Size*2 {
		return nil, release, Errorf(CodeUnauthorized, "missing or malformed %s", SignatureHeader)
	}
	if err := checkSignedPath(r.Header.Get(SignedPathHeader), r.URL.EscapedPath()); err != nil {
		return nil, release, Errorf(CodeUnauthorized, "%v", err)
	}
	if r.ContentLength > limit {
		return nil, release, Errorf(CodeBadRequest, "request is over %d bytes", limit)
	}
	hold := limit
	if r.ContentLength >= 0 {
		hold = r.ContentLength
	}
	if budget != nil {
		if !budget.take(hold) {
			e := Errorf(CodeUnavailable, "too many large requests at once; try again")
			e.Retryable = true
			return nil, release, e
		}
		var once sync.Once
		release = func() { once.Do(func() { budget.give(hold) }) }
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, hold+1))
	if err == nil && int64(len(body)) > hold {
		err = fmt.Errorf("request is over %d bytes", hold)
	}
	if err != nil {
		release()
		return nil, func() {}, Errorf(CodeBadRequest, "read request: %v", err)
	}
	err = VerifySignature(secret, r.Header.Get(TimestampHeader), r.Header.Get(SignatureHeader), r.Method,
		r.Header.Get(SignedPathHeader), r.URL.EscapedPath(), body, now)
	if err != nil {
		release()
		return nil, func() {}, Errorf(CodeUnauthorized, "%v", err)
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	return body, release, nil
}
