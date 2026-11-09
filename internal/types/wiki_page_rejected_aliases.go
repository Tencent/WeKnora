package types

import (
	"encoding/json"
	"strings"
)

// wikiPageMetadataRejectedAliases is the page_metadata key listing aliases a
// person removed from the page. Ingest and agents must not add them back.
const wikiPageMetadataRejectedAliases = "rejected_aliases"

// NormalizeWikiAlias returns the form aliases are compared in.
func NormalizeWikiAlias(alias string) string {
	return strings.ToLower(strings.TrimSpace(alias))
}

// RejectedAliases returns the aliases a person removed from this page.
func (p *WikiPage) RejectedAliases() []string {
	if p == nil || len(p.PageMetadata) == 0 {
		return nil
	}
	var meta map[string]json.RawMessage
	if err := json.Unmarshal(p.PageMetadata, &meta); err != nil {
		return nil
	}
	raw, ok := meta[wikiPageMetadataRejectedAliases]
	if !ok {
		return nil
	}
	var aliases []string
	if err := json.Unmarshal(raw, &aliases); err != nil {
		return nil
	}
	return aliases
}

// SetRejectedAliases stores the list in page_metadata and leaves every other
// metadata key as it was. An empty list removes the key.
func (p *WikiPage) SetRejectedAliases(aliases []string) error {
	meta := map[string]json.RawMessage{}
	if len(p.PageMetadata) > 0 {
		if err := json.Unmarshal(p.PageMetadata, &meta); err != nil {
			return err
		}
		if meta == nil {
			meta = map[string]json.RawMessage{}
		}
	}
	if len(aliases) == 0 {
		delete(meta, wikiPageMetadataRejectedAliases)
	} else {
		raw, err := json.Marshal(aliases)
		if err != nil {
			return err
		}
		meta[wikiPageMetadataRejectedAliases] = raw
	}
	encoded, err := json.Marshal(meta)
	if err != nil {
		return err
	}
	p.PageMetadata = JSON(encoded)
	return nil
}
