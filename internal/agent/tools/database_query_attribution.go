package tools

import (
	"strings"

	"github.com/Tencent/WeKnora/internal/utils"
)

func queryReferencesChunks(sqlQuery string) bool {
	parsed := utils.ParseSQL(sqlQuery)
	if parsed == nil || parsed.ParseError != "" {
		return false
	}
	for _, table := range parsed.TableNames {
		if strings.EqualFold(table, "chunks") {
			return true
		}
	}
	return false
}
