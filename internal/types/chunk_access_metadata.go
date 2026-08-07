package types

import (
	"encoding/json"
	"fmt"
)

const chunkAccessMetadataKey = "access_metadata"

// AccessMetadata returns only the reserved access metadata object for a chunk.
// Missing chunk metadata or a missing reserved key is represented by an empty
// map. A present reserved value must be a JSON object so malformed metadata
// cannot silently produce an unprotected index record.
func (c *Chunk) AccessMetadata() (JSONMap, error) {
	if c == nil || len(c.Metadata) == 0 {
		return JSONMap{}, nil
	}

	var metadata map[string]json.RawMessage
	if err := json.Unmarshal(c.Metadata, &metadata); err != nil {
		return nil, fmt.Errorf("decode chunk metadata: %w", err)
	}
	if metadata == nil {
		return nil, fmt.Errorf("chunk metadata must be a JSON object")
	}

	rawAccessMetadata, ok := metadata[chunkAccessMetadataKey]
	if !ok {
		return JSONMap{}, nil
	}

	var accessMetadata JSONMap
	if err := json.Unmarshal(rawAccessMetadata, &accessMetadata); err != nil {
		return nil, fmt.Errorf("decode %s: %w", chunkAccessMetadataKey, err)
	}
	if accessMetadata == nil {
		return nil, fmt.Errorf("%s must be a JSON object", chunkAccessMetadataKey)
	}
	return accessMetadata, nil
}

// withExistingAccessMetadata keeps the reserved access object while an
// existing metadata writer replaces its own fields. It deliberately carries
// no other Chunk.Metadata keys into the new payload.
func (c *Chunk) withExistingAccessMetadata(metadata []byte) (JSON, error) {
	if c == nil || len(c.Metadata) == 0 {
		return JSON(metadata), nil
	}

	var existing map[string]json.RawMessage
	if err := json.Unmarshal(c.Metadata, &existing); err != nil {
		return nil, fmt.Errorf("decode chunk metadata: %w", err)
	}
	if existing == nil {
		return nil, fmt.Errorf("chunk metadata must be a JSON object")
	}
	rawAccessMetadata, ok := existing[chunkAccessMetadataKey]
	if !ok {
		return JSON(metadata), nil
	}

	var accessMetadata JSONMap
	if err := json.Unmarshal(rawAccessMetadata, &accessMetadata); err != nil {
		return nil, fmt.Errorf("decode %s: %w", chunkAccessMetadataKey, err)
	}
	if accessMetadata == nil {
		return nil, fmt.Errorf("%s must be a JSON object", chunkAccessMetadataKey)
	}

	var replacement map[string]json.RawMessage
	if err := json.Unmarshal(metadata, &replacement); err != nil {
		return nil, fmt.Errorf("decode replacement chunk metadata: %w", err)
	}
	replacement[chunkAccessMetadataKey] = rawAccessMetadata
	merged, err := json.Marshal(replacement)
	if err != nil {
		return nil, fmt.Errorf("encode chunk metadata: %w", err)
	}
	return JSON(merged), nil
}
