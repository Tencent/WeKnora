package types

import (
	"path"
	"strings"
)

// ArtifactKind is a presentation hint, not permission to execute active content.
type ArtifactKind string

const (
	ArtifactPresentation ArtifactKind = "presentation"
	ArtifactWebPage      ArtifactKind = "web_page"
	ArtifactSpreadsheet  ArtifactKind = "spreadsheet"
	ArtifactDocument     ArtifactKind = "document"
	ArtifactImage        ArtifactKind = "image"
	ArtifactText         ArtifactKind = "text"
	ArtifactOther        ArtifactKind = "other"
)

func ArtifactKindForFile(name string) ArtifactKind {
	switch strings.ToLower(path.Ext(name)) {
	case ".pptx":
		return ArtifactPresentation
	case ".html", ".htm":
		return ArtifactWebPage
	case ".xlsx", ".xls", ".csv", ".tsv":
		return ArtifactSpreadsheet
	case ".pdf", ".docx":
		return ArtifactDocument
	case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".bmp", ".avif":
		return ArtifactImage
	case ".txt", ".md", ".markdown", ".json", ".yaml", ".yml", ".log", ".py", ".js", ".ts", ".go", ".sh":
		return ArtifactText
	default:
		return ArtifactOther
	}
}

// DisplayKind supplies the additive type marker for historical artifacts too.
// Ignore inconsistent persisted hints: a kind must agree with the file suffix.
func (a MessageArtifact) DisplayKind() ArtifactKind {
	return ArtifactKindForFile(a.FileName)
}
