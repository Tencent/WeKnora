//go:build anydoc && cgo

package anydoc

import (
	"errors"
	"fmt"
	"testing"

	upstream "github.com/firecrawl/anydoc/go"
)

func TestMarkEncryptedTypedErrors(t *testing.T) {
	for _, kind := range []string{"encrypted", "malformed", "resource_limit"} {
		t.Run(kind, func(t *testing.T) {
			cause := &upstream.ConvertError{Kind: kind, Detail: "converter diagnostic"}
			err := fmt.Errorf("convert: %w", cause)
			got := markEncrypted(err)
			if errors.Is(got, ErrEncrypted) != (kind == "encrypted") {
				t.Fatalf("markEncrypted(%v) = %v, wrong encryption classification", err, got)
			}
			if !errors.Is(got, cause) {
				t.Fatalf("markEncrypted(%v) lost the converter diagnostic: %v", err, got)
			}
		})
	}
	untyped := errors.New("anydoc: encrypted: document is encrypted")
	if got := markEncrypted(untyped); got != untyped {
		t.Fatalf("markEncrypted(%v) = %v, want unchanged error", untyped, got)
	}
}
