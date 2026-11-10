package allocation

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
)

// UserMachine is the logical identity of one shared machine: one owner, one
// sandbox config. It is comparable so it can key maps. ConfigID must already
// be normalized (sandbox.NormalizeConfigID) and ConfigTenantID resolved.
type UserMachine struct {
	OwnerTenantID  uint64
	OwnerID        string
	ConfigTenantID uint64
	ConfigID       string
}

// Validate rejects identities that cannot address a machine.
func (m UserMachine) Validate() error {
	if m.OwnerTenantID == 0 {
		return errors.New("allocation: user machine requires an owner tenant")
	}
	if err := ValidateOwnerID(m.OwnerID); err != nil {
		return err
	}
	if m.ConfigTenantID == 0 {
		return errors.New("allocation: user machine requires a resolved config tenant")
	}
	if strings.TrimSpace(m.ConfigID) == "" {
		return errors.New("allocation: user machine requires a config id")
	}
	return nil
}

// Digest is the hex SHA-256 of a length-prefixed canonical encoding, so no
// two identities share a digest by field-boundary ambiguity. The namespace is
// part of it: two deployments sharing one Redis never address each other's
// machines. It is the Redis hash tag, the DB key and the provider metadata.
func (m UserMachine) Digest(namespace string) string {
	h := sha256.New()
	write := func(s string) {
		var n [8]byte
		binary.BigEndian.PutUint64(n[:], uint64(len(s)))
		h.Write(n[:])
		h.Write([]byte(s))
	}
	write("weknora-user-sandbox/v1")
	write(namespace)
	write(strconv.FormatUint(m.OwnerTenantID, 10))
	write(m.OwnerID)
	write(strconv.FormatUint(m.ConfigTenantID, 10))
	write(m.ConfigID)
	return hex.EncodeToString(h.Sum(nil))
}
