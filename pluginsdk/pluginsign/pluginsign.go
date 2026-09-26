// Package pluginsign signs and verifies WeKnora plugin packages.
//
// A signed package carries plugin.sig at its root: a JSON document naming the
// signing key and holding an ed25519 signature over the package's content
// digest. The content digest covers every file in the package except
// plugin.sig itself, by path and SHA-256, so the signature survives
// re-zipping but not any change to a file.
//
// WeKnora trusts a signature only when its key is in the platform's trust
// store; the key's entry there decides the trust level (official or
// verified). An unsigned package, or one signed by a key the platform does
// not know, is a community package.
package pluginsign

import (
	"archive/zip"
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"path"
	"sort"
	"strings"
)

// SignatureFile is the signature's name at the package root.
const SignatureFile = "plugin.sig"

// manifestFile is the manifest's name at the package root.
const manifestFile = "plugin.yaml"

// Algorithm is the only signature algorithm today.
const Algorithm = "ed25519"

// PublicKeyPrefix starts a public key in its text form.
const PublicKeyPrefix = "ed25519:"

// domain separates plugin signatures from anything else the key might sign.
const domain = "weknora-plugin-signature-v1"

// Signature is the content of plugin.sig.
type Signature struct {
	// KeyID names the signing key in the platform's trust store.
	KeyID     string `json:"keyId"`
	Algorithm string `json:"algorithm"`
	// ContentDigest is "sha256:<hex>" over the package files; see
	// ContentDigest.
	ContentDigest string `json:"contentDigest"`
	// Signature is the base64 ed25519 signature.
	Signature string `json:"signature"`
}

// Errors Verify returns for a signature that does not hold.
var (
	ErrContentChanged = errors.New("the package contents do not match its signature")
	ErrBadSignature   = errors.New("the package signature does not verify with its key")
)

// ContentDigest fingerprints a package's files: SHA-256 over the sorted
// lines "<path>\x00<sha256 hex>\n", leaving out plugin.sig.
func ContentDigest(files map[string][]byte) string {
	names := make([]string, 0, len(files))
	for name := range files {
		if name != SignatureFile {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	h := sha256.New()
	for _, name := range names {
		sum := sha256.Sum256(files[name])
		_, _ = fmt.Fprintf(h, "%s\x00%s\n", name, hex.EncodeToString(sum[:]))
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil))
}

func message(keyID, contentDigest string) []byte {
	return []byte(domain + "\x00" + keyID + "\x00" + contentDigest)
}

// Read returns a package's signature, or nil when it is unsigned.
func Read(files map[string][]byte) (*Signature, error) {
	raw, ok := files[SignatureFile]
	if !ok {
		return nil, nil
	}
	var s Signature
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, fmt.Errorf("%s: %w", SignatureFile, err)
	}
	if s.Algorithm != Algorithm {
		return nil, fmt.Errorf("%s: algorithm %q is not supported", SignatureFile, s.Algorithm)
	}
	if s.KeyID == "" || s.ContentDigest == "" || s.Signature == "" {
		return nil, fmt.Errorf("%s needs keyId, contentDigest and signature", SignatureFile)
	}
	return &s, nil
}

// Verify checks a signature against the package files and the public key
// its KeyID names.
func (s *Signature) Verify(files map[string][]byte, pub ed25519.PublicKey) error {
	if ContentDigest(files) != s.ContentDigest {
		return ErrContentChanged
	}
	sig, err := base64.StdEncoding.DecodeString(s.Signature)
	if err != nil || len(pub) != ed25519.PublicKeySize || !ed25519.Verify(pub, message(s.KeyID, s.ContentDigest), sig) {
		return ErrBadSignature
	}
	return nil
}

// Sign returns the signature of a package's files.
func Sign(files map[string][]byte, keyID string, priv ed25519.PrivateKey) (*Signature, error) {
	if strings.TrimSpace(keyID) == "" {
		return nil, errors.New("a key ID is required")
	}
	digest := ContentDigest(files)
	return &Signature{
		KeyID: keyID, Algorithm: Algorithm, ContentDigest: digest,
		Signature: base64.StdEncoding.EncodeToString(ed25519.Sign(priv, message(keyID, digest))),
	}, nil
}

// SignArchive signs a package archive and returns it with plugin.sig added
// (replacing any earlier signature). The manifest must sit at the archive
// root.
func SignArchive(archive []byte, keyID string, priv ed25519.PrivateKey) ([]byte, error) {
	zr, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		return nil, fmt.Errorf("package is not a zip archive: %w", err)
	}
	files, err := readZip(zr)
	if err != nil {
		return nil, err
	}
	if _, ok := files[manifestFile]; !ok {
		return nil, fmt.Errorf("package has no %s at its root", manifestFile)
	}
	sig, err := Sign(files, keyID, priv)
	if err != nil {
		return nil, err
	}
	sigJSON, _ := json.MarshalIndent(sig, "", "  ")

	var out bytes.Buffer
	zw := zip.NewWriter(&out)
	for _, f := range zr.File {
		if name, _ := cleanName(f.Name); name == SignatureFile {
			continue
		}
		if err := zw.Copy(f); err != nil {
			return nil, err
		}
	}
	w, err := zw.Create(SignatureFile)
	if err != nil {
		return nil, err
	}
	if _, err := w.Write(append(sigJSON, '\n')); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// ReadArchive returns a package archive's files the way WeKnora sees them:
// paths relative to the package root, a single top-level directory holding
// everything stripped.
func ReadArchive(archive []byte) (map[string][]byte, error) {
	zr, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		return nil, fmt.Errorf("package is not a zip archive: %w", err)
	}
	files, err := readZip(zr)
	if err != nil {
		return nil, err
	}
	return StripSingleRoot(files), nil
}

func readZip(zr *zip.Reader) (map[string][]byte, error) {
	files := map[string][]byte{}
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		name, err := cleanName(f.Name)
		if err != nil {
			return nil, err
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		b, err := io.ReadAll(rc)
		_ = rc.Close()
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", name, err)
		}
		files[name] = b
	}
	return files, nil
}

func cleanName(name string) (string, error) {
	n := strings.ReplaceAll(name, "\\", "/")
	clean := path.Clean(n)
	if strings.HasPrefix(n, "/") || clean == ".." || strings.HasPrefix(clean, "../") || clean == "." {
		return "", fmt.Errorf("package entry %q escapes the package root", name)
	}
	return clean, nil
}

// StripSingleRoot drops a single top-level directory shared by every file,
// unless the manifest already sits at the root. WeKnora opens packages the
// same way.
func StripSingleRoot(files map[string][]byte) map[string][]byte {
	if _, ok := files[manifestFile]; ok || len(files) == 0 {
		return files
	}
	var root string
	for name := range files {
		top, _, found := strings.Cut(name, "/")
		if !found || (root != "" && top != root) {
			return files
		}
		root = top
	}
	out := make(map[string][]byte, len(files))
	for name, b := range files {
		out[strings.TrimPrefix(name, root+"/")] = b
	}
	return out
}

// FormatPublicKey is a public key in its text form, "ed25519:<base64>", as
// the platform's trust store lists it.
func FormatPublicKey(pub ed25519.PublicKey) string {
	return PublicKeyPrefix + base64.StdEncoding.EncodeToString(pub)
}

// ParsePublicKey reads a public key in its text form.
func ParsePublicKey(s string) (ed25519.PublicKey, error) {
	raw, ok := strings.CutPrefix(strings.TrimSpace(s), PublicKeyPrefix)
	if !ok {
		return nil, fmt.Errorf("public key must start with %q", PublicKeyPrefix)
	}
	b, err := base64.StdEncoding.DecodeString(raw)
	if err != nil || len(b) != ed25519.PublicKeySize {
		return nil, errors.New("public key is not a base64 ed25519 key")
	}
	return ed25519.PublicKey(b), nil
}

// MarshalPrivateKey encodes a private key as PKCS#8 PEM.
func MarshalPrivateKey(priv ed25519.PrivateKey) ([]byte, error) {
	der, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), nil
}

// ParsePrivateKey reads a PKCS#8 PEM ed25519 private key.
func ParsePrivateKey(data []byte) (ed25519.PrivateKey, error) {
	block, _ := pem.Decode(data)
	if block == nil || block.Type != "PRIVATE KEY" {
		return nil, errors.New("private key is not a PKCS#8 PEM block")
	}
	k, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	priv, ok := k.(ed25519.PrivateKey)
	if !ok {
		return nil, errors.New("private key is not an ed25519 key")
	}
	return priv, nil
}
