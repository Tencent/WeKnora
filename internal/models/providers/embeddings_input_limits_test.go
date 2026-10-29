package providers_test

import (
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/api"
	modelruntime "github.com/Tencent/WeKnora/internal/models/runtime"
	"github.com/Tencent/WeKnora/internal/types"
)

// embeddingCeilingVendors returns the registered vendors that document a
// per-request embedding ceiling, the way batch.go's limits are meant to be
// fed (api/batch.go: "A zero field means the vendor states no limit, and
// nothing is enforced for it").
func embeddingCeilingVendors(t *testing.T) []*modelruntime.Provider {
	t.Helper()
	var vendors []*modelruntime.Provider
	for _, v := range modelruntime.List() {
		if v.Compat.Embeddings.MaxBatchSize != nil {
			vendors = append(vendors, v)
		}
	}
	if len(vendors) == 0 {
		t.Fatal("no vendor documents embedding ceilings; the catalog drifted")
	}
	return vendors
}

// A vendor that documents how many texts one embedding request may carry
// must also document how long each text may be. batch.go enforces
// MaxInputChars only where a vendor declares it, and an undeclared input
// length let the chunker's 7499-rune protected chunks reach the provider
// unprechecked; the provider's 400 then failed the whole document after five
// retries (Tencent/WeKnora#4236).
func TestEmbeddingCeilingVendorsDeclareInputLimits(t *testing.T) {
	for _, v := range embeddingCeilingVendors(t) {
		if v.Compat.Embeddings.MaxInputChars == nil || *v.Compat.Embeddings.MaxInputChars <= 0 {
			t.Errorf("%s: declares max_batch_size %d but no max_input_chars; "+
				"oversized chunks reach the provider unprechecked",
				v.ID, *v.Compat.Embeddings.MaxBatchSize)
		}
	}
}

// The same rule binds one level down. Catalog rows carry their own
// max_batch_size where one vendor-wide figure fits no model — aliyun's
// text-embedding rows batch at 10 or 25, zhipu's embedding-3 at 64 — and
// batch.go merges row compat over vendor compat, so a row that caps how
// many texts a request carries must equally cap how long each text may be.
// An unpaired row-level batch size left aliyun and zhipu resolving with
// MaxItems set and MaxItemRunes zero: the precheck never fired for those
// deployments and 7499-rune protected chunks reached the provider
// unprechecked (Tencent/WeKnora#4236).
func TestEmbeddingCatalogRowsDeclareInputLimits(t *testing.T) {
	checked := 0
	for _, v := range modelruntime.List() {
		for _, m := range v.Models() {
			if m.Type != types.ModelTypeEmbedding || len(m.Compat) == 0 {
				continue
			}
			overlay := &api.EmbeddingsCompat{}
			if err := modelruntime.DecodeCompat(m.Compat, overlay); err != nil {
				t.Errorf("%s/%s%s: decode compat: %v", v.ID, m.ID, m.Match, err)
				continue
			}
			if overlay.MaxBatchSize == nil {
				continue
			}
			checked++
			if overlay.MaxInputChars == nil || *overlay.MaxInputChars <= 0 {
				t.Errorf("%s/%s%s: declares max_batch_size %d but no max_input_chars; "+
					"oversized chunks reach the provider unprechecked",
					v.ID, m.ID, m.Match, *overlay.MaxBatchSize)
			}
		}
	}
	if checked == 0 {
		t.Fatal("no catalog embedding row declares max_batch_size; the catalog drifted")
	}
}

// The chunker's protected-span ceiling (splitter.go, maxProtectedSize =
// absoluteMaxSize = 7500) leaves chunks of up to 7499 runes, and its
// separator-exhausted path (splitter.go:270) emits whole runs of unbounded
// length — the shapes #4236 reports. A ceiling vendor's resolved settings
// must reject locally, in SplitBatches, anything past its own documented
// limit, instead of spending five provider retries on a request the vendor
// can never accept.
func TestCeilingVendorsRejectOversizedChunkLocally(t *testing.T) {
	const protectedCeiling = 7500 // splitter.go maxProtectedSize/absoluteMaxSize

	for _, v := range embeddingCeilingVendors(t) {
		model := firstEmbeddingModel(v)
		if model == "" {
			t.Errorf("%s: documents embedding ceilings but lists no embedding model", v.ID)
			continue
		}
		r, err := modelruntime.Resolve(modelruntime.Ref{
			Provider: v.ID, Model: model, ModelType: types.ModelTypeEmbedding,
		})
		if err != nil {
			t.Errorf("%s/%s: resolve: %v", v.ID, model, err)
			continue
		}
		limits := r.Embeddings.BatchLimits()
		if limits.MaxItemRunes <= 0 {
			t.Errorf("%s/%s: resolved without a per-document limit; SplitBatches cannot precheck", v.ID, model)
			continue
		}

		// One rune past the documented limit must die locally.
		_, err = api.SplitBatches([]string{strings.Repeat("字", limits.MaxItemRunes+1)}, 0, limits)
		if err == nil {
			t.Errorf("%s/%s: a %d-rune chunk passed the precheck and would go to the provider",
				v.ID, model, limits.MaxItemRunes+1)
		} else if !strings.Contains(err.Error(), "per-document limit") {
			t.Errorf("%s/%s: precheck error does not name the per-document limit: %v", v.ID, model, err)
		}

		// Where the documented limit sits inside the chunker's protected-span
		// ceiling, the exact chunk #4236 reports must die locally too — that
		// is the case that otherwise burns five provider retries per document.
		if limits.MaxItemRunes < protectedCeiling {
			_, err = api.SplitBatches([]string{strings.Repeat("字", protectedCeiling-1)}, 0, limits)
			if err == nil {
				t.Errorf("%s/%s: a %d-rune protected chunk passed the precheck (limit %d)",
					v.ID, model, protectedCeiling-1, limits.MaxItemRunes)
			}
		}
	}
}

// firstEmbeddingModel names a catalog embedding row for the vendor, so the
// vendor compat is resolved the way a real model row resolves.
func firstEmbeddingModel(v *modelruntime.Provider) string {
	for _, m := range v.Models() {
		if m.Type == types.ModelTypeEmbedding {
			if m.ID != "" {
				return m.ID
			}
			if m.Match != "" {
				return strings.ReplaceAll(m.Match, "*", "x")
			}
		}
	}
	return ""
}
