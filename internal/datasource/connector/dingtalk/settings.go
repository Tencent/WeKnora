package dingtalk

import (
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
)

// settingBool interprets a data source settings value as a boolean, returning
// def when the value is absent or of an unrecognised shape. Settings arrive
// from JSON, so a hand-edited config may carry either a real bool or its string
// form. The accepted spellings are the ones the Yuque connector's settingBool
// accepts, so every connector reads its switches the same way.
func settingBool(v interface{}, def bool) bool {
	switch t := v.(type) {
	case bool:
		return t
	case string:
		switch strings.ToLower(strings.TrimSpace(t)) {
		case "true", "1", "yes", "on":
			return true
		case "false", "0", "no", "off":
			return false
		}
	}
	return def
}

// documentSettings holds the optional ingestion switches carried in
// DataSourceConfig.Settings.
//
// Every switch defaults to false: a data source created before the switch
// existed keeps the behaviour it had then until an operator turns the switch on
// deliberately, so an upgrade never starts downloading or embedding content
// nobody asked for.
type documentSettings struct {
	// IncludeUploadedFiles admits uploaded Office/PDF files (FILE/DOCUMENT
	// nodes) that are downloaded from 钉盘 and parsed by WeKnora itself.
	IncludeUploadedFiles bool
	// PreserveFolderStructure encodes each document's DingTalk folder hierarchy
	// into its FileName, so the document lands in the knowledge base folder
	// that mirrors where it lives in the wiki instead of at the root.
	//
	// It is an ingest-path switch, not a migration. An already-ingested
	// document is only re-read when its content changes, so turning the switch
	// on does not relocate it; a full sync does.
	PreserveFolderStructure bool
}

// parseDocumentSettings reads the settings bag of a data source. A missing or
// malformed value leaves the switch off, so a hand-edited config can only ever
// opt in to an ingest path, never alter one by accident.
func parseDocumentSettings(dataSourceConfig *types.DataSourceConfig) documentSettings {
	var raw map[string]interface{}
	if dataSourceConfig != nil {
		raw = dataSourceConfig.Settings
	}
	return documentSettings{
		IncludeUploadedFiles:    settingBool(raw["include_uploaded_files"], false),
		PreserveFolderStructure: settingBool(raw["preserve_folder_structure"], false),
	}
}
