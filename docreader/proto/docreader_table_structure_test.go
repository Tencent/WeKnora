package proto

import (
	"testing"

	"google.golang.org/protobuf/proto"
)

func TestReadConfigTableStructureRoundTrip(t *testing.T) {
	in := &ReadConfig{
		EnableTableStructure:    true,
		TableStructureFileTypes: []string{"docx", "xlsx"},
	}
	data, err := proto.Marshal(in)
	if err != nil {
		t.Fatalf("marshal ReadConfig: %v", err)
	}

	var out ReadConfig
	if err := proto.Unmarshal(data, &out); err != nil {
		t.Fatalf("unmarshal ReadConfig: %v", err)
	}
	if !out.GetEnableTableStructure() {
		t.Fatalf("EnableTableStructure was not preserved after round trip")
	}
	if got := out.GetTableStructureFileTypes(); len(got) != 2 || got[0] != "docx" || got[1] != "xlsx" {
		t.Fatalf("TableStructureFileTypes was not preserved after round trip: %#v", got)
	}
}
