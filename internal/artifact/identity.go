package artifact

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/google/uuid"
)

const StableEntityIDVersion uint16 = 1

var weknoraEntityNamespace = uuid.MustParse("8298b3ec-ccef-5ae4-a326-7e29c2d07b41")

// EntityIdentityInput contains only semantic identity fields. Position or a
// global sequence number must never be used as SourceAnchor.
type EntityIdentityInput struct {
	KnowledgeID      string
	IDVersion        uint16
	EntityType       string
	ParentSemanticID string
	SourceAnchor     string
	Content          string
	DuplicateOrdinal uint32
}

type EntityIdentity struct {
	ID            string
	MatchKey      string
	SemanticKey   string
	ContentDigest string
}

// StableEntityIdentity returns a UUIDv5 scoped to the knowledge namespace.
func StableEntityIdentity(input EntityIdentityInput) (EntityIdentity, error) {
	if strings.TrimSpace(input.KnowledgeID) == "" {
		return EntityIdentity{}, errors.New("entity knowledge ID must not be empty")
	}
	if input.IDVersion == 0 {
		return EntityIdentity{}, errors.New("entity ID version must not be zero")
	}
	if strings.TrimSpace(input.EntityType) == "" {
		return EntityIdentity{}, errors.New("entity type must not be empty")
	}
	if strings.TrimSpace(input.SourceAnchor) == "" {
		return EntityIdentity{}, errors.New("entity source anchor must not be empty")
	}

	contentDigest := SHA256Hex([]byte(input.Content))
	matchMaterial := strings.Join([]string{
		"weknora.entity-match.v1",
		strconv.FormatUint(uint64(input.IDVersion), 10),
		input.EntityType,
		input.ParentSemanticID,
		input.SourceAnchor,
		strconv.FormatUint(uint64(input.DuplicateOrdinal), 10),
	}, "\x00")
	matchKey := SHA256Hex([]byte(matchMaterial))
	semanticMaterial := strings.Join([]string{
		"weknora.entity-id.v1",
		matchKey,
		contentDigest,
	}, "\x00")
	semanticKey := SHA256Hex([]byte(semanticMaterial))

	namespace := uuid.NewSHA1(weknoraEntityNamespace, []byte(input.KnowledgeID))
	if parsed, err := uuid.Parse(input.KnowledgeID); err == nil {
		namespace = parsed
	}
	id := uuid.NewSHA1(namespace, []byte(semanticMaterial))
	return EntityIdentity{
		ID:            id.String(),
		MatchKey:      matchKey,
		SemanticKey:   semanticKey,
		ContentDigest: contentDigest,
	}, nil
}

// IdentityAllocator supplies a local duplicate ordinal only when all other
// identity fields are identical.
type IdentityAllocator struct {
	knowledgeID string
	idVersion   uint16
	occurrences map[string]uint32
}

func NewIdentityAllocator(knowledgeID string, idVersion uint16) *IdentityAllocator {
	return &IdentityAllocator{
		knowledgeID: knowledgeID,
		idVersion:   idVersion,
		occurrences: make(map[string]uint32),
	}
}

func (a *IdentityAllocator) Next(
	entityType string,
	parentSemanticID string,
	sourceAnchor string,
	content string,
) (EntityIdentity, error) {
	if a == nil {
		return EntityIdentity{}, errors.New("entity identity allocator must not be nil")
	}
	occurrenceKey := strings.Join([]string{
		entityType,
		parentSemanticID,
		sourceAnchor,
		SHA256Hex([]byte(content)),
	}, "\x00")
	ordinal := a.occurrences[occurrenceKey]
	a.occurrences[occurrenceKey] = ordinal + 1
	return StableEntityIdentity(EntityIdentityInput{
		KnowledgeID:      a.knowledgeID,
		IDVersion:        a.idVersion,
		EntityType:       entityType,
		ParentSemanticID: parentSemanticID,
		SourceAnchor:     sourceAnchor,
		Content:          content,
		DuplicateOrdinal: ordinal,
	})
}

// ReuseUniqueLegacyID preserves an existing random UUID when exactly one live
// entity has the same semantic key. Ambiguous matches deliberately fall back to
// the deterministic ID.
func ReuseUniqueLegacyID(
	desired EntityIdentity,
	existing map[string][]string,
) (string, bool, error) {
	ids := existing[desired.SemanticKey]
	if len(ids) != 1 {
		return desired.ID, false, nil
	}
	if _, err := uuid.Parse(ids[0]); err != nil {
		return "", false, fmt.Errorf("legacy entity ID %q is not a UUID: %w", ids[0], err)
	}
	return ids[0], true, nil
}
