// Package trust decides how far the platform trusts a plugin package.
//
// A package signed by a key in the platform's trust store gets that key's
// level: official (the WeKnora team) or verified (a reviewed marketplace or
// publisher the operator vouches for). Anything else, unsigned or signed by
// an unknown key, is community. A signature that does not hold (a changed
// file, a forged signature, a key used for a publisher it may not sign for)
// is refused outright.
//
// The trust store is a YAML file named by WEKNORA_PLUGIN_TRUSTED_KEYS;
// WEKNORA_PLUGIN_MIN_TRUST raises the lowest level the platform installs and
// loads (default community: everything).
package trust

import (
	"crypto/ed25519"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/Tencent/WeKnora/internal/plugin/pkg"
	"github.com/Tencent/WeKnora/pluginsdk/pluginsign"
)

// Level is how far a package is trusted.
type Level string

// Levels from least to most trusted.
const (
	Community Level = "community"
	Verified  Level = "verified"
	Official  Level = "official"
)

func (l Level) rank() int {
	switch l {
	case Official:
		return 2
	case Verified:
		return 1
	default:
		return 0
	}
}

// AtLeast reports whether l is floor or more trusted.
func (l Level) AtLeast(floor Level) bool { return l.rank() >= floor.rank() }

// ParseLevel reads a level; empty is community.
func ParseLevel(s string) (Level, error) {
	switch l := Level(strings.ToLower(strings.TrimSpace(s))); l {
	case "":
		return Community, nil
	case Community, Verified, Official:
		return l, nil
	default:
		return "", fmt.Errorf("trust level %q is not community, verified or official", s)
	}
}

// Key is a signing key the platform trusts.
type Key struct {
	ID        string
	PublicKey ed25519.PublicKey
	Level     Level
	// Publishers limits the key to packages of these publisher IDs; empty
	// lets it sign any.
	Publishers []string
}

// Verdict is how far one package is trusted.
type Verdict struct {
	Level Level `json:"level"`
	// KeyID names the key that signed the package, trusted or not.
	KeyID string `json:"keyId,omitempty"`
	// Trusted says the signing key is in the trust store.
	Trusted bool `json:"trusted,omitempty"`
}

// BelowMinimumError is a package the platform's minimum trust level refuses.
type BelowMinimumError struct {
	Level, Min Level
}

func (e *BelowMinimumError) Error() string {
	return fmt.Sprintf("this platform only accepts %s plugins or better; this package is %s", e.Min, e.Level)
}

// Store is the platform's trusted keys and minimum level.
type Store struct {
	keys map[string]Key
	min  Level
}

// NewStore creates a Store.
func NewStore(keys []Key, floor Level) (*Store, error) {
	s := &Store{keys: map[string]Key{}, min: floor}
	for _, k := range keys {
		if k.ID == "" || len(k.PublicKey) != ed25519.PublicKeySize {
			return nil, fmt.Errorf("trusted key %q needs an ID and an ed25519 public key", k.ID)
		}
		if k.Level != Verified && k.Level != Official {
			return nil, fmt.Errorf("trusted key %s: level must be verified or official", k.ID)
		}
		if _, dup := s.keys[k.ID]; dup {
			return nil, fmt.Errorf("trusted key %s is listed twice", k.ID)
		}
		s.keys[k.ID] = k
	}
	return s, nil
}

// fileFormat is the trust store file.
type fileFormat struct {
	Keys []struct {
		ID         string   `yaml:"id"`
		PublicKey  string   `yaml:"publicKey"`
		Level      string   `yaml:"level"`
		Publishers []string `yaml:"publishers"`
	} `yaml:"keys"`
}

// Parse reads a trust store file.
func Parse(data []byte, floor Level) (*Store, error) {
	var f fileFormat
	if err := yaml.Unmarshal(data, &f); err != nil {
		return nil, err
	}
	keys := make([]Key, 0, len(f.Keys))
	for _, k := range f.Keys {
		pub, err := pluginsign.ParsePublicKey(k.PublicKey)
		if err != nil {
			return nil, fmt.Errorf("trusted key %s: %w", k.ID, err)
		}
		level, err := ParseLevel(k.Level)
		if err != nil {
			return nil, fmt.Errorf("trusted key %s: %w", k.ID, err)
		}
		keys = append(keys, Key{ID: k.ID, PublicKey: pub, Level: level, Publishers: k.Publishers})
	}
	return NewStore(keys, floor)
}

// FromEnv loads the store WEKNORA_PLUGIN_TRUSTED_KEYS and
// WEKNORA_PLUGIN_MIN_TRUST describe. With neither set, every package is
// community and every package is accepted.
func FromEnv() (*Store, error) {
	floor, err := ParseLevel(os.Getenv("WEKNORA_PLUGIN_MIN_TRUST"))
	if err != nil {
		return nil, fmt.Errorf("WEKNORA_PLUGIN_MIN_TRUST: %w", err)
	}
	path := strings.TrimSpace(os.Getenv("WEKNORA_PLUGIN_TRUSTED_KEYS"))
	if path == "" {
		return NewStore(nil, floor)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("WEKNORA_PLUGIN_TRUSTED_KEYS: %w", err)
	}
	s, err := Parse(data, floor)
	if err != nil {
		return nil, fmt.Errorf("WEKNORA_PLUGIN_TRUSTED_KEYS %s: %w", path, err)
	}
	return s, nil
}

// Min is the lowest level the platform accepts.
func (s *Store) Min() Level { return s.min }

// Evaluate returns a package's trust level, or an error when its signature
// is present but does not hold.
func (s *Store) Evaluate(p *pkg.Package) (Verdict, error) {
	sig := p.Signature
	if sig == nil {
		return Verdict{Level: Community}, nil
	}
	k, ok := s.keys[sig.KeyID]
	if !ok {
		// A key the platform does not know vouches for nothing, but the
		// contents must still be what was signed.
		if err := p.VerifySignature(nil); errors.Is(err, pluginsign.ErrContentChanged) {
			return Verdict{}, err
		}
		return Verdict{Level: Community, KeyID: sig.KeyID}, nil
	}
	if err := p.VerifySignature(k.PublicKey); err != nil {
		return Verdict{}, err
	}
	if len(k.Publishers) > 0 && !slices.Contains(k.Publishers, p.Manifest.Publisher.ID) {
		return Verdict{}, fmt.Errorf("key %s may not sign plugins of publisher %q", k.ID, p.Manifest.Publisher.ID)
	}
	return Verdict{Level: k.Level, KeyID: k.ID, Trusted: true}, nil
}

// Admit refuses a level below the platform's minimum.
func (s *Store) Admit(l Level) error {
	if l == "" {
		l = Community
	}
	if !l.AtLeast(s.min) {
		return &BelowMinimumError{Level: l, Min: s.min}
	}
	return nil
}

// Check evaluates a package and admits it: the gate an install and a load
// both pass.
func (s *Store) Check(p *pkg.Package) (Verdict, error) {
	v, err := s.Evaluate(p)
	if err != nil {
		return v, err
	}
	return v, s.Admit(v.Level)
}
