package pluginapi

import (
	"strconv"
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
	sig := Sign(secret, now.Unix(), body)
	if err := VerifySignature(secret, ts, sig, body, now); err != nil {
		t.Fatal(err)
	}
	if VerifySignature(secret, ts, sig, []byte(`{"a":2}`), now) == nil {
		t.Fatal("a changed body must fail")
	}
	if VerifySignature(secret, ts, sig, body, now.Add(10*time.Minute)) == nil {
		t.Fatal("a replayed old request must fail")
	}
}

func TestErrorStatus(t *testing.T) {
	if Errorf(CodeRateLimited, "x").Code.HTTPStatus() != 429 || !Errorf(CodeUnavailable, "x").Retryable ||
		Errorf(CodeInternal, "x").Retryable {
		t.Fatal("error codes map wrong")
	}
}
