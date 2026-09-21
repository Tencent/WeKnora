package api

import (
	"context"
	"fmt"
)

// EmbeddingAPI names an embedding wire protocol. Like RerankAPI it is its own
// type, so a chat row's extra_config.api cannot name an embedding dialect and
// pass validation.
type EmbeddingAPI string

// The embedding protocols WeKnora speaks.
const (
	// EmbeddingOpenAI is POST {base}/embeddings with {model, input[]} answering
	// {data: [{embedding, index}]} — the shape OpenAI defined and every
	// compatible gateway copied. Jina, NVIDIA, Azure and WeKnora Cloud speak
	// it too, with their own optional fields, URL or credential.
	EmbeddingOpenAI EmbeddingAPI = "openai-embeddings"
	// EmbeddingDashScope is Alibaba Model Studio's native multimodal shape:
	// input.contents, parameters.dimension, output.embeddings.
	EmbeddingDashScope EmbeddingAPI = "dashscope-embeddings"
	// EmbeddingArk is Volcengine Ark's multimodal shape, which fuses every
	// input into one vector and therefore embeds one text per request.
	EmbeddingArk EmbeddingAPI = "ark-embeddings"
	// EmbeddingGoogle is Gemini's batchEmbedContents.
	EmbeddingGoogle EmbeddingAPI = "google-embeddings"
)

// Known reports whether the value names a protocol this build implements.
func (a EmbeddingAPI) Known() bool {
	switch a {
	case EmbeddingOpenAI, EmbeddingDashScope, EmbeddingArk, EmbeddingGoogle:
		return true
	}
	return false
}

// EmbedInputType distinguishes the two sides of a retrieval pair. Several
// vendors score them differently and want to be told which one they are
// embedding; the field they want it in differs, so the catalog names it.
type EmbedInputType string

const (
	// EmbedDocument is text being indexed.
	EmbedDocument EmbedInputType = "document"
	// EmbedQuery is text being searched with.
	EmbedQuery EmbedInputType = "query"
)

// Embedder is what every embedding protocol package implements. Callers pass
// the whole batch; splitting it to the vendor's documented ceiling is the
// shared layer's job, not the protocol's.
type Embedder interface {
	Embed(ctx context.Context, texts []string, kind EmbedInputType) ([][]float32, error)
}

// PlaceEmbeddings puts returned vectors back in the order the texts were
// sent, using the index each vendor reports, and refuses a response that does
// not cover every input.
//
// Both halves matter. A vendor is free to answer out of order, so the index
// has to be honoured rather than assumed — and reading the wrong field for it
// silently collapses a whole batch onto slot 0, which is what
// Tencent/WeKnora#3484 was. An unfilled slot then reaches the index as an
// empty vector, where it is stored and poisons retrieval without anything
// failing, so a gap is an error instead. So is an index reported twice: one
// of the two vectors belongs to some other input.
func PlaceEmbeddings(want, got int, at func(i int) (int, []float32)) ([][]float32, error) {
	out := make([][]float32, want)
	seen := make([]bool, want)
	for i := 0; i < got; i++ {
		index, vector := at(i)
		if index < 0 || index >= want {
			return nil, fmt.Errorf("embedding index %d out of range for %d inputs", index, want)
		}
		if seen[index] {
			return nil, fmt.Errorf("embedding index %d returned twice", index)
		}
		seen[index] = true
		out[index] = vector
	}
	for i, vector := range out {
		if len(vector) == 0 {
			return nil, fmt.Errorf("no embedding returned for input %d of %d", i, want)
		}
	}
	return out, nil
}
