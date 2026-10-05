// Package checkpoint persists completed expensive parsing and embedding work.
package checkpoint

import (
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
)

// Key identifies the exact inputs whose result can replace another provider call.
func Key(v any) string {
	b, _ := json.Marshal(v)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// Path returns the persistent location, or an empty path when checkpoints are disabled.
func Path(kind, key string) string {
	root := os.Getenv("WEKNORA_PROCESSING_CHECKPOINT_DIR")
	if root == "" {
		return ""
	}
	return filepath.Join(root, kind, key+".json.gz")
}

// Load reads a completed checkpoint; missing or incomplete files are cache misses.
func Load(kind, key string, dst any) bool {
	p := Path(kind, key)
	if p == "" {
		return false
	}
	f, e := os.Open(p)
	if e != nil {
		return false
	}
	defer func() { _ = f.Close() }()
	z, e := gzip.NewReader(f)
	if e != nil {
		return false
	}
	defer func() { _ = z.Close() }()
	return json.NewDecoder(z).Decode(dst) == nil
}

// Save atomically publishes a completed compressed checkpoint.
func Save(kind, key string, value any) error {
	p := Path(kind, key)
	if p == "" {
		return nil
	}
	if e := os.MkdirAll(filepath.Dir(p), 0o700); e != nil {
		return e
	}
	f, e := os.CreateTemp(filepath.Dir(p), ".checkpoint-")
	if e != nil {
		return e
	}
	defer func() { _ = os.Remove(f.Name()) }()
	z := gzip.NewWriter(f)
	e = json.NewEncoder(z).Encode(value)
	if ce := z.Close(); e == nil {
		e = ce
	}
	if ce := f.Close(); e == nil {
		e = ce
	}
	if e != nil {
		return e
	}
	return os.Rename(f.Name(), p)
}
