package pluginsign

import (
	"archive/zip"
	"bytes"
	"crypto/ed25519"
	"errors"
	"testing"
)

func archive(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte(body))
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestSignArchiveRoundTrip(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)
	signed, err := SignArchive(archive(t, map[string]string{
		"plugin.yaml": "id: acme.x\n", "bin/x": "binary", SignatureFile: "stale",
	}), "acme-2026", priv)
	if err != nil {
		t.Fatal(err)
	}
	files, err := ReadArchive(signed)
	if err != nil {
		t.Fatal(err)
	}
	sig, err := Read(files)
	if err != nil || sig == nil {
		t.Fatalf("read signature: %v %v", sig, err)
	}
	if sig.KeyID != "acme-2026" {
		t.Fatalf("key id = %q", sig.KeyID)
	}
	if err := sig.Verify(files, pub); err != nil {
		t.Fatalf("verify: %v", err)
	}

	other, _, _ := ed25519.GenerateKey(nil)
	if err := sig.Verify(files, other); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("another key verifies: %v", err)
	}
	files["bin/x"] = []byte("tampered")
	if err := sig.Verify(files, pub); !errors.Is(err, ErrContentChanged) {
		t.Fatalf("a changed file verifies: %v", err)
	}
}

func TestContentDigestIgnoresSignatureAndOrder(t *testing.T) {
	a := ContentDigest(map[string][]byte{"a": []byte("1"), "b": []byte("2")})
	b := ContentDigest(map[string][]byte{"b": []byte("2"), "a": []byte("1"), SignatureFile: []byte("x")})
	if a != b {
		t.Fatalf("digests differ: %s %s", a, b)
	}
	// A renamed file is a different package.
	if c := ContentDigest(map[string][]byte{"a": []byte("1"), "c": []byte("2")}); c == a {
		t.Fatal("rename kept the digest")
	}
}

func TestSignArchiveNeedsRootManifest(t *testing.T) {
	_, priv, _ := ed25519.GenerateKey(nil)
	if _, err := SignArchive(archive(t, map[string]string{"x/plugin.yaml": ""}), "k", priv); err == nil {
		t.Fatal("signed an archive whose manifest is not at the root")
	}
}

func TestKeyEncodings(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)
	got, err := ParsePublicKey(FormatPublicKey(pub))
	if err != nil || !got.Equal(pub) {
		t.Fatalf("public key round trip: %v", err)
	}
	pemBytes, err := MarshalPrivateKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	back, err := ParsePrivateKey(pemBytes)
	if err != nil || !back.Equal(priv) {
		t.Fatalf("private key round trip: %v", err)
	}
	if _, err := ParsePublicKey("rsa:abc"); err == nil {
		t.Fatal("accepted a non-ed25519 key")
	}
}

func TestReadRejectsMalformedSignature(t *testing.T) {
	if s, err := Read(map[string][]byte{}); s != nil || err != nil {
		t.Fatalf("unsigned package: %v %v", s, err)
	}
	for _, body := range []string{
		"{",
		`{"algorithm":"rsa","keyId":"k","contentDigest":"d","signature":"s"}`,
		`{"algorithm":"ed25519"}`,
	} {
		if _, err := Read(map[string][]byte{SignatureFile: []byte(body)}); err == nil {
			t.Fatalf("accepted %s", body)
		}
	}
}
