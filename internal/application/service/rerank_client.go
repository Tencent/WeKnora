package service

// rerank_client.go — P3 strangler: the caller-side rerank/ASR seams over the
// unified invoke entry. Rerank: the six non-signed vendors (openai-shape
// fallback, jina, zhipu, nvidia, aliyun, weknoracloud) ride invoke.Rerank;
// lkeap (TC3) and volcengine (IAM) stay on the v1 SDK clients until P5 and
// are branched in GetRerankModel. ASR: every vendor rides invoke.Transcribe
// (the v1 asr package is deleted; the caller seam moved to types/interfaces).

import (
	"context"

	"github.com/Tencent/WeKnora/internal/models/invoke"
	"github.com/Tencent/WeKnora/internal/models/rerank"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// invokeReranker implements rerank.Reranker over invoke.Rerank.
type invokeReranker struct {
	cfg *invoke.ModelConfig
}

// newInvokeReranker assembles the caller-side reranker from the shared
// constructor's ModelConfig.
func newInvokeReranker(cfg *invoke.ModelConfig) *invokeReranker {
	return &invokeReranker{cfg: cfg}
}

// Rerank maps the unified (index, score) results back onto the v1 caller
// shape. The v1 callers saw the vendor's echoed document text; vendors echo
// the input back, so re-deriving it by index yields the same content (P2
// review precedent: document re-derivation is a recorded non-gated delta).
func (r *invokeReranker) Rerank(
	ctx context.Context, query string, documents []string,
) ([]rerank.RankResult, error) {
	resp, err := invoke.Rerank(ctx, r.cfg, &invoke.RerankOptions{
		Query:     query,
		Documents: documents,
	})
	if err != nil {
		return nil, err
	}
	out := make([]rerank.RankResult, 0, len(resp.Results))
	for _, res := range resp.Results {
		doc := ""
		if res.Index >= 0 && res.Index < len(documents) {
			doc = documents[res.Index]
		}
		out = append(out, rerank.RankResult{
			Index:          res.Index,
			Document:       rerank.DocumentInfo{Text: doc},
			RelevanceScore: res.Score,
		})
	}
	return out, nil
}

// GetModelName returns the wire model name.
func (r *invokeReranker) GetModelName() string { return r.cfg.ModelName }

// GetModelID returns the model record ID.
func (r *invokeReranker) GetModelID() string { return r.cfg.ModelID }

// invokeASR implements interfaces.ASR over invoke.Transcribe.
type invokeASR struct {
	cfg *invoke.ModelConfig
}

// newInvokeASR assembles the caller-side ASR client from the shared
// constructor's ModelConfig.
func newInvokeASR(cfg *invoke.ModelConfig) *invokeASR {
	return &invokeASR{cfg: cfg}
}

// Transcribe maps the unified response onto the v1 caller shape.
func (a *invokeASR) Transcribe(
	ctx context.Context, audioBytes []byte, fileName string,
) (*interfaces.TranscriptionResult, error) {
	resp, err := invoke.Transcribe(ctx, a.cfg, &invoke.ASROptions{
		Audio:    audioBytes,
		FileName: fileName,
	})
	if err != nil {
		return nil, err
	}
	out := &interfaces.TranscriptionResult{Text: resp.Text}
	for _, seg := range resp.Segments {
		out.Segments = append(out.Segments, interfaces.Segment{
			Start: seg.Start,
			End:   seg.End,
			Text:  seg.Text,
		})
	}
	return out, nil
}

// GetModelName returns the wire model name.
func (a *invokeASR) GetModelName() string { return a.cfg.ModelName }

// GetModelID returns the model record ID.
func (a *invokeASR) GetModelID() string { return a.cfg.ModelID }
