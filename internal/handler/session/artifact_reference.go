package session

import (
	"net/url"
	"path"
	"regexp"
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
)

// Models routinely reference the files they generated in the sandbox from
// their final answer, most often as a Markdown image (`![评分](市场画像评分.html)`).
// A bare file name resolves to nothing once the sandbox is gone, so this is the
// boundary where an answer's transient, sandbox-local references are normalized
// into the one reference form that survives: `resource://<handle>`.
//
// The handle is the same identity every other stored file uses (knowledge-base
// images, chat attachments), which is what makes a saved answer keep working:
// re-binding the handle to a knowledge entry is enough, with no copy and no
// second rewrite. It is also opaque — the physical storage path stays server
// side, and every read still goes through an authorizing proxy.
//
// A deployment without the resource catalog has no handle to point at, so those
// references are normalized to the canonical `sandbox:<name>` spelling instead
// and resolved by name against the message's own artifact list. That keeps the
// answer readable in the chat, and only there.
//
// The rewrite runs once per turn, after ArtifactCollector has drained
// /workspace/output, so every artifact already has its handle.

// fencedOrInlineCodeRE splits content so destinations inside code samples are
// never rewritten — a documentation snippet showing the syntax must survive.
var fencedOrInlineCodeRE = regexp.MustCompile("(?s)(```.*?```|~~~.*?~~~|`[^`\n]*`)")

// schemeRE matches an already-qualified URL (http://, resource://, data:, …).
var schemeRE = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9+.\-]*:`)

// titleSuffixRE splits the optional title off a link destination
// (`file.png "caption"`).
var titleSuffixRE = regexp.MustCompile(`(?s)^(.*?)(\s+(?:"[^"]*"|'[^']*'))$`)

// rewriteArtifactReferences replaces every Markdown link/image destination that
// names one of the turn's artifacts with that artifact's stable reference.
//
// Three destination spellings are accepted, because models are inconsistent
// about the prefix even when the prompt asks for one:
//
//	![评分](sandbox:市场画像评分.html)
//	![评分](市场画像评分.html)
//	![评分](./output/市场画像评分.html)
//
// Anything else — an http URL, a resource:// handle the model copied from
// context, an unknown file name — is returned unchanged so existing behaviour
// (knowledge-base images, web links) is untouched.
func rewriteArtifactReferences(content string, artifacts types.MessageArtifacts) string {
	if content == "" || len(artifacts) == 0 {
		return content
	}
	byName := artifactRefByName(artifacts)
	if len(byName) == 0 {
		return content
	}

	parts := fencedOrInlineCodeRE.Split(content, -1)
	if len(parts) == 1 {
		return rewriteArtifactReferencesInSegment(content, byName)
	}
	code := fencedOrInlineCodeRE.FindAllString(content, -1)
	var out strings.Builder
	out.Grow(len(content))
	for i, part := range parts {
		out.WriteString(rewriteArtifactReferencesInSegment(part, byName))
		if i < len(code) {
			out.WriteString(code[i])
		}
	}
	return out.String()
}

// rewriteArtifactReferencesInSegment walks every inline Markdown link/image in
// one non-code segment and rebinds the ones that name an artifact.
//
// Destinations are located by matching parentheses rather than by regex,
// because skill-generated file names routinely contain both spaces and
// parentheses (`腾讯控股(00700) 成交量_838ccc.html`). A regex that stops at the
// first space or paren would capture half the name and never match.
func rewriteArtifactReferencesInSegment(segment string, byName map[string]string) string {
	if segment == "" || !strings.Contains(segment, "](") {
		return segment
	}

	var out strings.Builder
	out.Grow(len(segment))
	cursor := 0
	for cursor < len(segment) {
		relative := strings.Index(segment[cursor:], "](")
		if relative < 0 {
			break
		}
		closeBracket := cursor + relative
		open := closeBracket + 1

		inner, end, ok := scanLinkDestination(segment, open)
		if !ok || !hasLinkLabelBefore(segment, closeBracket) {
			out.WriteString(segment[cursor : open+1])
			cursor = open + 1
			continue
		}

		destination, title := splitDestinationTitle(inner)
		ref, matched := lookupArtifactRef(destination, byName, markdownImageBefore(segment, closeBracket))
		if !matched {
			out.WriteString(segment[cursor : end+1])
			cursor = end + 1
			continue
		}

		out.WriteString(segment[cursor : open+1])
		out.WriteString(ref)
		out.WriteString(title)
		out.WriteString(")")
		cursor = end + 1
	}
	out.WriteString(segment[cursor:])
	return out.String()
}

// scanLinkDestination returns the text between the `(` at openIndex and its
// matching `)`, plus the index of that closing paren. A destination never spans
// a line break, so a newline ends the scan unsuccessfully.
func scanLinkDestination(text string, openIndex int) (string, int, bool) {
	depth := 1
	for i := openIndex + 1; i < len(text); i++ {
		switch text[i] {
		case '\n':
			return "", 0, false
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return text[openIndex+1 : i], i, true
			}
		}
	}
	return "", 0, false
}

// hasLinkLabelBefore reports whether `](` is preceded by a `[` on the same
// line, i.e. whether this really is a link rather than incidental punctuation.
func hasLinkLabelBefore(text string, closeBracketIndex int) bool {
	for i := closeBracketIndex - 1; i >= 0; i-- {
		switch text[i] {
		case '\n':
			return false
		case '[':
			return true
		}
	}
	return false
}

// splitDestinationTitle separates `dest "title"` into its two parts, returning
// the title with its leading whitespace so it can be re-emitted verbatim.
func splitDestinationTitle(inner string) (string, string) {
	if groups := titleSuffixRE.FindStringSubmatch(inner); groups != nil {
		return strings.TrimSpace(groups[1]), groups[2]
	}
	return strings.TrimSpace(inner), ""
}

func markdownImageBefore(text string, closeBracketIndex int) bool {
	for i := closeBracketIndex - 1; i >= 0; i-- {
		switch text[i] {
		case '\n':
			return false
		case '[':
			return i > 0 && text[i-1] == '!'
		}
	}
	return false
}

func looksLikeSandboxOutputPath(candidate string) bool {
	c := strings.TrimPrefix(strings.TrimSpace(candidate), "./")
	c = strings.TrimPrefix(c, "/")
	return strings.HasPrefix(c, "workspace/output/") || strings.HasPrefix(c, "output/")
}

// lookupArtifactRef resolves one Markdown destination to an artifact reference.
func lookupArtifactRef(destination string, byName map[string]string, image bool) (string, bool) {
	candidate := strings.TrimSpace(destination)
	if candidate == "" {
		return "", false
	}
	// Trim the optional angle-bracket form Markdown allows around a
	// destination: `[x](<name with space>)` — kept for completeness even
	// though the outer regex rejects inner whitespace.
	candidate = strings.TrimPrefix(strings.TrimSuffix(candidate, ">"), "<")

	hadSandboxPrefix := false
	for _, prefix := range []string{"sandbox://", "sandbox:"} {
		if len(candidate) >= len(prefix) && strings.EqualFold(candidate[:len(prefix)], prefix) {
			candidate = candidate[len(prefix):]
			hadSandboxPrefix = true
			break
		}
	}
	// A destination that already carries a scheme is a real URL, not a file
	// name. `sandbox:` is the one exception and was stripped above.
	if !hadSandboxPrefix && schemeRE.MatchString(candidate) {
		return "", false
	}

	// Models percent-encode non-ASCII names about half the time.
	if decoded, err := url.PathUnescape(candidate); err == nil {
		candidate = decoded
	}
	candidate = strings.TrimSpace(candidate)
	// Bare names are rewritten for images (`![chart](a.html)`) because that is
	// how models actually cite generated files. Ordinary links (`[docs](README.md)`)
	// are left alone unless they already carry a sandbox prefix or path —
	// otherwise a colliding artifact name would hijack a real hyperlink.
	if !hadSandboxPrefix && !image && !looksLikeSandboxOutputPath(candidate) {
		return "", false
	}
	// Directory prefixes (`./`, `output/`, `/workspace/output/`) carry no
	// information the artifact list does not already have.
	candidate = path.Base(candidate)
	if candidate == "" || candidate == "." || candidate == "/" {
		return "", false
	}

	ref, ok := byName[candidate]
	return ref, ok
}

// artifactRefByName maps each artifact's file name to the reference that
// replaces it in the answer. A duplicate name keeps the first occurrence:
// silently preferring the later file would make the reference point at
// something the model did not describe.
func artifactRefByName(artifacts types.MessageArtifacts) map[string]string {
	byName := make(map[string]string, len(artifacts))
	for _, artifact := range artifacts {
		name := strings.TrimSpace(artifact.FileName)
		if name == "" {
			continue
		}
		if _, exists := byName[name]; exists {
			continue
		}
		byName[name] = artifactReference(artifact)
	}
	return byName
}

// artifactReference returns the destination an answer should carry for one
// artifact: its catalog handle when the deployment has a resource registry,
// otherwise the canonical chat-only `sandbox:<name>` form.
//
// The raw storage path is never a candidate. It names a bucket and key, which
// is both an information leak and useless to a client that has no credentials
// for the object store.
func artifactReference(artifact types.MessageArtifact) string {
	if handle, ok := types.ParseResourcePath(artifact.URL); ok {
		return types.BuildResourcePath(handle)
	}
	return "sandbox:" + strings.TrimSpace(artifact.FileName)
}
