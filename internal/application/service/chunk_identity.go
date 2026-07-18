package service

import (
	"strconv"

	"github.com/google/uuid"
)

// Stable namespace for ingestion-derived UUIDs. Never change this value once
// released: doing so would invalidate all chunk and citation identities.
var ingestionArtifactNamespace = uuid.MustParse("111f15f1-cbb5-5da7-9a97-4da6fd01eec7")

type stableChunkIdentity struct {
	KnowledgeID string
	ChunkType   string
	Content     string
	Occurrence  int
}

func stableChunkID(identity stableChunkIdentity) string {
	contentHash := hashBytes([]byte(canonicalizeArtifactText(identity.Content)))
	key := hashFingerprint(
		"chunk-v1",
		identity.KnowledgeID,
		identity.ChunkType,
		artifactCanonicalTextVersion,
		contentHash,
		strconv.Itoa(identity.Occurrence),
	)
	return uuid.NewSHA1(ingestionArtifactNamespace, []byte(key)).String()
}

func stableDerivedID(knowledgeID string, parts ...string) string {
	all := append([]string{"derived-v1", knowledgeID}, parts...)
	return uuid.NewSHA1(ingestionArtifactNamespace, []byte(hashFingerprint(all...))).String()
}
