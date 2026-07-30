package core

import (
	"encoding/json"
	"net/http"
	"os"
	"testing"

	secutils "github.com/Tencent/WeKnora/internal/utils"
)

func TestMain(m *testing.M) {
	os.Setenv("SSRF_WHITELIST", "127.0.0.1,localhost")
	secutils.ResetSSRFWhitelistForTest()
	os.Exit(m.Run())
}

func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

// blk constructors mirroring wiki/connector_golden_test.go, for core-internal
// block-rendering tests (DocxBlock is same-package here, no core. prefix).
func sheetBlk(id, token string) DocxBlock {
	return DocxBlock{BlockID: id, BlockType: BlockTypeSheet, Sheet: &BlockTokenRef{Token: token}}
}
func bitableBlk(id, token string) DocxBlock {
	return DocxBlock{BlockID: id, BlockType: BlockTypeBitable, Bitable: &BlockTokenRef{Token: token}}
}
func imageBlk(id, token string) DocxBlock {
	return DocxBlock{BlockID: id, BlockType: BlockTypeImage, Image: &BlockTokenRef{Token: token}}
}
func fileBlk(id, token, name string) DocxBlock {
	return DocxBlock{BlockID: id, BlockType: BlockTypeFile, File: &BlockFileRef{Token: token, Name: name}}
}
func cellBlk(id string) DocxBlock {
	return DocxBlock{BlockID: id, BlockType: BlockTypeTableCell, Children: []string{id + "_txt"}}
}
func cellTextBlk(id, text string) DocxBlock {
	return DocxBlock{BlockID: id + "_txt", BlockType: BlockTypeText, Text: txt(text)}
}
func tableBlk(id string, cols int, cellIDs ...string) DocxBlock {
	b := DocxBlock{BlockID: id, BlockType: BlockTypeTable}
	b.Table = &BlockTable{Cells: cellIDs}
	b.Table.Property = &BlockTableProperty{ColumnSize: cols}
	return b
}
