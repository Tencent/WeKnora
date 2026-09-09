package knowledge

import (
	"fmt"
	"strings"
)

// VideoKnowledgeIndexSourceRefs returns the source document references already
// validated as part of a video index page's frontmatter.
func VideoKnowledgeIndexSourceRefs(content string) []string {
	frontmatter, _ := parseWikiFrontmatter(content)
	return stringSliceValue(frontmatter["source_refs"])
}

// ValidateVideoKnowledgeIndexPage owns the storage and identity contract for
// the fixed video/<video-id> Wiki index namespace.
func ValidateVideoKnowledgeIndexPage(
	content string,
	pageType string,
	slug string,
	expectedVideoID string,
	expectedGeneration string,
	expectedTitle string,
) error {
	if strings.TrimSpace(pageType) != "index" {
		return fmt.Errorf("video index page_type must be index")
	}
	slug = strings.TrimSpace(slug)
	if !strings.HasPrefix(slug, "video/") || strings.Count(slug, "/") != 1 {
		return fmt.Errorf("video index slug must be video/<video-id>")
	}
	videoID := strings.TrimSpace(strings.TrimPrefix(slug, "video/"))
	if videoID == "" {
		return fmt.Errorf("video index slug must contain a video ID")
	}
	if expectedVideoID = strings.TrimSpace(expectedVideoID); expectedVideoID != "" && videoID != expectedVideoID {
		return fmt.Errorf("video index slug does not match the active video")
	}
	if strings.TrimSpace(content) == "" {
		return fmt.Errorf("video index content is empty")
	}
	frontmatter, _ := parseWikiFrontmatter(content)
	if declaredPageType := strings.ToLower(strings.TrimSpace(stringValue(frontmatter["page_type"]))); declaredPageType != "" && declaredPageType != "index" {
		return fmt.Errorf("frontmatter.page_type must be index")
	}
	if strings.ToLower(strings.TrimSpace(stringValue(frontmatter["type"]))) != "knowledge_base" {
		return fmt.Errorf("frontmatter.type must be knowledge_base")
	}
	if strings.TrimSpace(stringValue(frontmatter["source_video_id"])) != videoID {
		return fmt.Errorf("frontmatter.source_video_id must match the video index slug")
	}
	generation := strings.TrimSpace(stringValue(frontmatter["transcript_generation"]))
	if generation == "" {
		return fmt.Errorf("frontmatter.transcript_generation is required")
	}
	if expectedGeneration = strings.TrimSpace(expectedGeneration); expectedGeneration != "" && generation != expectedGeneration {
		return fmt.Errorf("frontmatter.transcript_generation does not match the active generation")
	}
	if strings.ToLower(strings.TrimSpace(stringValue(frontmatter["audit_status"]))) != "aligned" {
		return fmt.Errorf("frontmatter.audit_status must be aligned")
	}
	indexTitle := strings.TrimSpace(stringValue(frontmatter["title"]))
	if indexTitle == "" {
		return fmt.Errorf("frontmatter.title is required")
	}
	if expectedTitle = strings.TrimSpace(expectedTitle); expectedTitle != "" && indexTitle != expectedTitle+"_知识底座" {
		return fmt.Errorf("frontmatter.title must be %s_知识底座", expectedTitle)
	}
	return nil
}
