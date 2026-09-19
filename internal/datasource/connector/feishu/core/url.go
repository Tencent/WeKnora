package core

import (
	"net/url"
	"strings"
)

// Link kinds parsed from a Feishu/Lark document URL path.
const (
	LinkKindWiki    = "wiki"
	LinkKindDocx    = "docx"
	LinkKindDoc     = "doc"
	LinkKindSheet   = "sheet"
	LinkKindBitable = "bitable"
	LinkKindFile    = "file"
)

// Reject reasons for URLs the links connector will not sync. Stable codes so
// the UI can localise them; ListResources copies them into Resource.Metadata.
const (
	RejectWikiSpace    = "wiki_space"
	RejectDriveFolder  = "drive_folder"
	RejectUnrecognized = "unrecognized"
)

// ParsedDocURL is one user-supplied Feishu/Lark document URL after path
// classification. Token is the path segment (wiki node_token or drive
// doc_token); ObjType is filled later by GetWikiNode / batch_query.
type ParsedDocURL struct {
	Original     string
	Kind         string
	Token        string
	RejectReason string
	Skip         bool
}

// ParseFeishuDocURL classifies a pasted Feishu/Lark cloud-doc URL by path.
// Host is ignored (tenant subdomains like ruijie.feishu.cn work). Query and
// fragment are stripped when extracting the token.
func ParseFeishuDocURL(raw string) ParsedDocURL {
	original := strings.TrimSpace(raw)
	if original == "" {
		return ParsedDocURL{Original: raw, Skip: true}
	}

	parsed, err := url.Parse(original)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return ParsedDocURL{Original: original, RejectReason: RejectUnrecognized}
	}

	segs := splitPath(parsed.Path)
	return classifyPath(original, segs)
}

func splitPath(path string) []string {
	path = strings.Trim(path, "/")
	if path == "" {
		return nil
	}
	parts := strings.Split(path, "/")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func classifyPath(original string, segs []string) ParsedDocURL {
	for i, s := range segs {
		switch s {
		case "wiki":
			if i+1 >= len(segs) {
				return ParsedDocURL{Original: original, RejectReason: RejectUnrecognized}
			}
			if segs[i+1] == "space" {
				return ParsedDocURL{Original: original, RejectReason: RejectWikiSpace}
			}
			return ParsedDocURL{Original: original, Kind: LinkKindWiki, Token: segs[i+1]}
		case "drive":
			if i+1 < len(segs) && segs[i+1] == "folder" {
				return ParsedDocURL{Original: original, RejectReason: RejectDriveFolder}
			}
		case "docx":
			return tokenAfter(original, segs, i, LinkKindDocx)
		case "docs":
			return tokenAfter(original, segs, i, LinkKindDoc)
		case "sheets":
			return tokenAfter(original, segs, i, LinkKindSheet)
		case "base":
			return tokenAfter(original, segs, i, LinkKindBitable)
		case "file":
			return tokenAfter(original, segs, i, LinkKindFile)
		}
	}
	return ParsedDocURL{Original: original, RejectReason: RejectUnrecognized}
}

func tokenAfter(original string, segs []string, i int, kind string) ParsedDocURL {
	if i+1 >= len(segs) || segs[i+1] == "" {
		return ParsedDocURL{Original: original, RejectReason: RejectUnrecognized}
	}
	return ParsedDocURL{Original: original, Kind: kind, Token: segs[i+1]}
}

// DedupeParsedURLs keeps the first occurrence of each Kind+Token (or, for
// rejected URLs, each reject-reason + path without query). Empty lines are
// dropped. duplicateCount is how many later copies were discarded.
func DedupeParsedURLs(parsed []ParsedDocURL) (unique []ParsedDocURL, duplicateCount int) {
	seen := make(map[string]struct{}, len(parsed))
	for _, p := range parsed {
		if p.Skip {
			continue
		}
		key := parsedURLKey(p)
		if _, ok := seen[key]; ok {
			duplicateCount++
			continue
		}
		seen[key] = struct{}{}
		unique = append(unique, p)
	}
	return unique, duplicateCount
}

func parsedURLKey(p ParsedDocURL) string {
	if p.Kind != "" && p.Token != "" {
		return p.Kind + ":" + p.Token
	}
	return p.RejectReason + ":" + stripURLQuery(p.Original)
}

func stripURLQuery(raw string) string {
	if i := strings.IndexAny(raw, "?#"); i >= 0 {
		return raw[:i]
	}
	return raw
}

// LinkResourceID is the canonical resource id after resolution:
// "{obj_type}:{obj_token}". Wiki pages use obj_token (not node_token) so a
// /wiki/ copylink and a /docx/ link to the same document collapse.
func LinkResourceID(objType, objToken string) string {
	return objType + FeishuWikiNodeResourceSeparator + objToken
}

// ParseLinkResourceID splits "{obj_type}:{obj_token}".
func ParseLinkResourceID(resourceID string) (objType, objToken string) {
	objType, objToken, _ = strings.Cut(resourceID, FeishuWikiNodeResourceSeparator)
	return objType, objToken
}

// ExtractLinkURLs reads settings["urls"] which may be []string, []interface{},
// or a newline-separated string (frontend textarea).
func ExtractLinkURLs(settings map[string]interface{}) []string {
	if settings == nil {
		return nil
	}
	raw, ok := settings["urls"]
	if !ok || raw == nil {
		return nil
	}
	switch v := raw.(type) {
	case []string:
		return v
	case []interface{}:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	case string:
		return strings.Split(v, "\n")
	default:
		return nil
	}
}
