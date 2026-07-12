package service

import (
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

const artifactCacheTTL = 30 * 24 * time.Hour

func artifactModelKey(id, name string) string {
	if strings.TrimSpace(id) != "" {
		return id
	}
	return "name:" + name
}

// contentAddressedChunkID returns a UUID-shaped, deterministic identifier for
// a chunk. Position is part of the address so repeated boilerplate inside one
// document remains distinct, while rebuilding unchanged content preserves all
// vector, wiki and graph references.
func contentAddressedChunkID(knowledgeID, kind string, sequence int, content string) string {
	normalized := strings.Join(strings.Fields(strings.TrimSpace(content)), " ")
	address := strings.Join([]string{knowledgeID, kind, strconv.Itoa(sequence), normalized}, "\x00")
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte(address)).String()
}
