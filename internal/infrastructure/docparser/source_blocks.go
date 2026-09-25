package docparser

import (
	"encoding/json"

	"github.com/Tencent/WeKnora/docreader/proto"
	"github.com/Tencent/WeKnora/internal/types"
)

// wireSourceBlock is the transport shape of a source block: a markdown range
// plus the JSON-encoded locator, shared by the gRPC and HTTP docreaders.
type wireSourceBlock struct {
	Start       uint32 `json:"start"`
	End         uint32 `json:"end"`
	LocatorJSON string `json:"locator_json"`
}

// decodeSourceBlock converts one transported block, dropping it when the
// range is empty or the locator cannot be decoded.
func decodeSourceBlock(b wireSourceBlock) (types.SourceBlock, bool) {
	if b.End <= b.Start || b.LocatorJSON == "" {
		return types.SourceBlock{}, false
	}
	var loc types.SourceLocator
	if err := json.Unmarshal([]byte(b.LocatorJSON), &loc); err != nil || loc.Type == "" {
		return types.SourceBlock{}, false
	}
	return types.SourceBlock{Start: int(b.Start), End: int(b.End), Locator: loc}, true
}

func sourceBlocksFromProto(pbs []*proto.SourceBlock) []types.SourceBlock {
	if len(pbs) == 0 {
		return nil
	}
	out := make([]types.SourceBlock, 0, len(pbs))
	for _, pb := range pbs {
		if b, ok := decodeSourceBlock(wireSourceBlock{
			Start: pb.GetStart(), End: pb.GetEnd(), LocatorJSON: pb.GetLocatorJson(),
		}); ok {
			out = append(out, b)
		}
	}
	return out
}

func sourceBlocksFromWire(wire []wireSourceBlock) []types.SourceBlock {
	if len(wire) == 0 {
		return nil
	}
	out := make([]types.SourceBlock, 0, len(wire))
	for _, w := range wire {
		if b, ok := decodeSourceBlock(w); ok {
			out = append(out, b)
		}
	}
	return out
}
