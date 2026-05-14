package text

// KnowledgeDisplayName picks the most informative human label for a
// knowledge entry. File-uploads use FileName; URL / text entries fall
// back to Title; the ID is the last-resort placeholder so a table cell
// is never empty. Single source for the ordering - `weknora doc list`
// and `weknora search docs` both call this so a Knowledge renders
// identically in either command.
//
// Takes the three fields directly rather than `sdk.Knowledge` so this
// package stays free of SDK imports.
func KnowledgeDisplayName(fileName, title, id string) string {
	if fileName != "" {
		return fileName
	}
	if title != "" {
		return title
	}
	return id
}
