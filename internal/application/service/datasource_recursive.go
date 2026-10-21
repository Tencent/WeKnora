package service

import (
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
)

func recursiveItemUnchanged(existing *types.Knowledge, item *types.FetchedItem) bool {
	if existing == nil || item == nil || existing.ParseStatus != types.ParseStatusCompleted ||
		item.Metadata["recursive"] != "true" || len(item.Content) == 0 || existing.FileHash == "" {
		return false
	}
	return existing.FileName == item.FileName && existing.Title == item.Title &&
		strings.EqualFold(existing.FileHash, calculateStr(string(item.Content)))
}
