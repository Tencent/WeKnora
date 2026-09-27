package pluginapi

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestHandshake(t *testing.T) {
	h := Handshake{Protocol: ProtocolVersion, Network: "unix", Address: "/tmp/p.sock"}
	got, ok, err := ParseHandshake(h.String() + "\n")
	if !ok || err != nil || got != h {
		t.Fatalf("round trip = %+v %v %v", got, ok, err)
	}
	if _, ok, _ := ParseHandshake("starting up..."); ok {
		t.Fatal("other output is not a handshake")
	}
	bad := []string{
		"WEKNORA_PLUGIN|2|unix|/x", "WEKNORA_PLUGIN|1|pipe|/x", "WEKNORA_PLUGIN|1|unix|", "WEKNORA_PLUGIN|1",
	}
	for _, bad := range bad {
		if _, ok, err := ParseHandshake(bad); !ok || err == nil {
			t.Errorf("%q must be a rejected handshake", bad)
		}
	}
}

func TestSignature(t *testing.T) {
	secret, body, now := []byte("k"), []byte(`{"a":1}`), time.Now()
	ts := strconv.FormatInt(now.Unix(), 10)
	path, other := "/p/acme.a/1.0.0/v1/search/s", "/p/acme.b/1.0.0/v1/search/s"
	sig := Sign(secret, now.Unix(), "POST", path, body)
	if err := VerifySignature(secret, ts, sig, "POST", path, path, body, now); err != nil {
		t.Fatal(err)
	}
	// A proxy in front may strip a prefix.
	if err := VerifySignature(secret, ts, sig, "POST", path, "/v1/search/s", body, now); err != nil {
		t.Fatal(err)
	}
	for name, err := range map[string]error{
		"changed body":       VerifySignature(secret, ts, sig, "POST", path, path, []byte(`{"a":2}`), now),
		"old request":        VerifySignature(secret, ts, sig, "POST", path, path, body, now.Add(10*time.Minute)),
		"other method":       VerifySignature(secret, ts, sig, "PUT", path, path, body, now),
		"other plugin":       VerifySignature(secret, ts, sig, "POST", path, other, body, now),
		"forged signed path": VerifySignature(secret, ts, sig, "POST", other, other, body, now),
		"no signed path":     VerifySignature(secret, ts, sig, "POST", "", path, body, now),
	} {
		if err == nil {
			t.Errorf("%s must fail", name)
		}
	}
}

// A signed body is read only after the cheap checks pass, within a size
// limit and a budget of bodies held at once.
func TestReadSigned(t *testing.T) {
	secret, now := []byte("k"), time.Now()
	request := func(body string, sign bool) *http.Request {
		r := httptest.NewRequest(http.MethodPost, "/v1/search/s", strings.NewReader(body))
		if sign {
			SignRequest(r, secret, []byte(body))
		}
		return r
	}
	if _, _, err := ReadSigned(request("{}", false), secret, 10, nil, now); err == nil || err.Code != CodeUnauthorized {
		t.Fatalf("unsigned: %v", err)
	}
	if _, _, err := ReadSigned(request("0123456789ab", true), secret, 10, nil, now); err == nil ||
		err.Code != CodeBadRequest {
		t.Fatalf("over the limit: %v", err)
	}
	budget := NewBodyBudget(4)
	body, release, err := ReadSigned(request("abc", true), secret, 10, budget, now)
	if err != nil || string(body) != "abc" {
		t.Fatalf("signed: %q, %v", body, err)
	}
	if _, _, err := ReadSigned(request("xy", true), secret, 10, budget, now); err == nil || !err.Retryable {
		t.Fatalf("over the budget while the first body is held: %v", err)
	}
	release()
	release() // harmless twice
	if _, rel, err := ReadSigned(request("xy", true), secret, 10, budget, now); err != nil {
		t.Fatalf("after release: %v", err)
	} else {
		rel()
	}
	if budget.free != 4 {
		t.Fatalf("budget leaked: %d free", budget.free)
	}
}

func TestErrorStatus(t *testing.T) {
	if Errorf(CodeRateLimited, "x").Code.HTTPStatus() != 429 || !Errorf(CodeUnavailable, "x").Retryable ||
		Errorf(CodeInternal, "x").Retryable {
		t.Fatal("error codes map wrong")
	}
}
