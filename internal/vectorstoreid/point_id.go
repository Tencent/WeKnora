// Package vectorstoreid defines backend-neutral physical identities for
// vector-store rows. It does not cache or fingerprint embedding artifacts.
package vectorstoreid

import "github.com/google/uuid"

var pointNamespace = uuid.MustParse("d8500940-20fe-5dc8-8c4c-7c40aa6cc8d5")

// StablePointID returns a UUIDv5 physical point ID for one logical vector row.
func StablePointID(sourceID string) string {
	return uuid.NewSHA1(pointNamespace, []byte(sourceID)).String()
}

// CompatibleUUIDPointID preserves an already-valid UUID SourceID. This is
// useful for stores such as Weaviate where historical regular chunk points
// already used ChunkID == SourceID as their UUID, while non-UUID generated
// source IDs still need a deterministic physical UUID.
func CompatibleUUIDPointID(sourceID string) string {
	if parsed, err := uuid.Parse(sourceID); err == nil {
		return parsed.String()
	}
	return StablePointID(sourceID)
}
