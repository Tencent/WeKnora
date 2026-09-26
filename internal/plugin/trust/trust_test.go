package trust

import (
	"archive/zip"
	"bytes"
	"crypto/ed25519"
	"errors"
	"io"
	"testing"

	"github.com/Tencent/WeKnora/internal/plugin/pkg"
	"github.com/Tencent/WeKnora/internal/plugin/plugintest"
	"github.com/Tencent/WeKnora/pluginsdk/pluginsign"
)

func open(t *testing.T, data []byte) *pkg.Package {
	t.Helper()
	p, err := pkg.Open(data)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func signed(t *testing.T, keyID string, priv ed25519.PrivateKey) []byte {
	t.Helper()
	data, err := pluginsign.SignArchive(plugintest.KitPackage(t, "1.0.0"), keyID, priv)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// tamper rewrites one file of a signed archive, keeping plugin.sig.
func tamper(t *testing.T, data []byte, name, body string) []byte {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]string{}
	for _, f := range zr.File {
		rc, _ := f.Open()
		b, _ := io.ReadAll(rc)
		_ = rc.Close()
		files[f.Name] = string(b)
	}
	files[name] = body
	return plugintest.Zip(t, files)
}

func TestEvaluate(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)
	_, strangerPriv, _ := ed25519.GenerateKey(nil)
	store, err := NewStore(
		[]Key{{ID: "acme-2026", PublicKey: pub, Level: Verified, Publishers: []string{"acme"}}}, Community)
	if err != nil {
		t.Fatal(err)
	}

	if v, err := store.Evaluate(open(t, plugintest.KitPackage(t, "1.0.0"))); err != nil || v.Level != Community ||
		v.KeyID != "" {
		t.Fatalf("unsigned = %+v, %v", v, err)
	}
	if v, err := store.Evaluate(open(t, signed(t, "acme-2026", priv))); err != nil || v.Level != Verified ||
		!v.Trusted {
		t.Fatalf("trusted signature = %+v, %v", v, err)
	}
	if v, err := store.Evaluate(open(t, signed(t, "stranger", strangerPriv))); err != nil || v.Level != Community ||
		v.KeyID != "stranger" || v.Trusted {
		t.Fatalf("unknown key = %+v, %v", v, err)
	}
	// A known key ID with someone else's signature is a forgery.
	if _, err := store.Evaluate(open(t, signed(t, "acme-2026", strangerPriv))); !errors.Is(err,
		pluginsign.ErrBadSignature) {
		t.Fatalf("forged signature: %v", err)
	}
	changed := tamper(t, signed(t, "acme-2026", priv), "skills/triage/SKILL.md",
		"---\nname: triage\ndescription: changed\n---\n")
	if _, err := store.Evaluate(open(t, changed)); !errors.Is(err, pluginsign.ErrContentChanged) {
		t.Fatalf("changed contents: %v", err)
	}
	// Even an unknown key's signature must match the contents.
	changedStranger := tamper(t, signed(t, "stranger", strangerPriv), "skills/triage/SKILL.md",
		"---\nname: triage\ndescription: changed\n---\n")
	if _, err := store.Evaluate(open(t, changedStranger)); !errors.Is(err, pluginsign.ErrContentChanged) {
		t.Fatalf("changed contents under an unknown key: %v", err)
	}

	other, err := NewStore([]Key{{ID: "acme-2026", PublicKey: pub, Level: Verified, Publishers: []string{"globex"}}},
		Community)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := other.Evaluate(open(t, signed(t, "acme-2026", priv))); err == nil {
		t.Fatal("a key signed for a publisher it may not sign for")
	}
}

func TestAdmit(t *testing.T) {
	store, _ := NewStore(nil, Verified)
	var below *BelowMinimumError
	if err := store.Admit(Community); !errors.As(err, &below) || below.Min != Verified {
		t.Fatalf("community under a verified minimum: %v", err)
	}
	if err := store.Admit(""); err == nil {
		t.Fatal("an unrecorded level counts as community")
	}
	for _, l := range []Level{Verified, Official} {
		if err := store.Admit(l); err != nil {
			t.Fatalf("%s: %v", l, err)
		}
	}
}

func TestParse(t *testing.T) {
	pub, _, _ := ed25519.GenerateKey(nil)
	s, err := Parse([]byte("keys:\n  - id: weknora\n    publicKey: "+pluginsign.FormatPublicKey(pub)+
		"\n    level: official\n"), Verified)
	if err != nil || s.keys["weknora"].Level != Official || s.Min() != Verified {
		t.Fatalf("parse = %+v, %v", s, err)
	}
	for _, bad := range []string{
		"keys:\n  - id: x\n    publicKey: nope\n    level: official\n",
		"keys:\n  - id: x\n    publicKey: " + pluginsign.FormatPublicKey(pub) + "\n    level: community\n",
		"keys:\n  - id: x\n    publicKey: " + pluginsign.FormatPublicKey(pub) + "\n    level: verified\n" +
			"  - id: x\n    publicKey: " + pluginsign.FormatPublicKey(pub) + "\n    level: verified\n",
	} {
		if _, err := Parse([]byte(bad), Community); err == nil {
			t.Fatalf("accepted %q", bad)
		}
	}
	if _, err := ParseLevel("gold"); err == nil {
		t.Fatal("accepted an unknown level")
	}
}
