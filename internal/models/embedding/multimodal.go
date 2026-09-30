package embedding

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
)

// Modality aliases types.EmbeddingModality: types.IndexInfo carries the
// modality and cannot import this package, so the canonical declaration lives
// in types.
type Modality = types.EmbeddingModality

const (
	// ModalityText is plain text input.
	ModalityText = types.EmbeddingModalityText
	// ModalityImage is image input.
	ModalityImage = types.EmbeddingModalityImage
)

// ErrMultimodalUnsupported is returned when a model cannot accept image input
// at all. Callers use it to decide between failing fast and falling back to
// the text-only (OCR/caption) pipeline.
var ErrMultimodalUnsupported = errors.New("embedding model does not support multimodal input")

// ErrMixedModalityUnsupported is returned when a provider can embed text and
// images separately but cannot fold both into a single vector.
var ErrMixedModalityUnsupported = errors.New("embedding model cannot encode text and image into one vector")

// Capabilities describes what an embedding model can actually do.
// UnifiedSpace is the flag that matters for retrieval: only when text and image
// vectors land in the SAME space can a text query recall images.
type Capabilities struct {
	// Modalities lists the input modalities the model accepts.
	Modalities []Modality
	// UnifiedSpace reports whether all supported modalities share one vector
	// space, i.e. a text query can retrieve images directly.
	UnifiedSpace bool
}

// Supports reports whether the model accepts the given modality.
func (c Capabilities) Supports(m Modality) bool {
	for _, have := range c.Modalities {
		if have == m {
			return true
		}
	}
	return false
}

// ImagePart carries one image for embedding. Set Data when the bytes are in
// memory, URL when the image is already reachable by the provider.
type ImagePart struct {
	// Data is the raw encoded image (png/jpeg/webp/...).
	Data []byte
	// MIME is optional; when empty it is sniffed from Data.
	MIME string
	// URL is an already-reachable image reference. Used when Data is empty.
	URL string
}

// ImageRef returns the wire representation of the image: a data URI when bytes
// are available, otherwise the URL as-is.
func (p ImagePart) ImageRef() (string, error) {
	if len(p.Data) > 0 {
		mime := strings.TrimSpace(p.MIME)
		if mime == "" {
			mime = DetectImageMIME(p.Data)
		}
		return "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(p.Data), nil
	}
	if url := strings.TrimSpace(p.URL); url != "" {
		return url, nil
	}
	return "", errors.New("image part has neither data nor url")
}

// DetectImageMIME sniffs the MIME type of image bytes, falling back to
// image/jpeg — the only type every vision embedding endpoint accepts.
func DetectImageMIME(data []byte) string {
	if mime := http.DetectContentType(data); strings.HasPrefix(mime, "image/") {
		return mime
	}
	return "image/jpeg"
}

// Input is a single embedding request: Text and Images are folded
// into ONE vector, so callers needing separate vectors issue separate inputs.
// It is deliberately not a content-part list — providers disagree on whether
// mixed content is one vector or several.
type Input struct {
	// Text is optional when at least one image is present.
	Text string
	// Images is optional when Text is set.
	Images []ImagePart
}

// Validate rejects empty inputs and images that carry no payload at all.
func (in Input) Validate() error {
	if strings.TrimSpace(in.Text) == "" && len(in.Images) == 0 {
		return errors.New("embedding input is empty: need text or at least one image")
	}
	for i, img := range in.Images {
		if len(img.Data) == 0 && strings.TrimSpace(img.URL) == "" {
			return fmt.Errorf("embedding input image[%d] has neither data nor url", i)
		}
	}
	return nil
}

// HasImage reports whether the input carries any image content.
func (in Input) HasImage() bool { return len(in.Images) > 0 }

// IsMixed reports whether the input combines text and image content.
func (in Input) IsMixed() bool {
	return strings.TrimSpace(in.Text) != "" && len(in.Images) > 0
}

// MultimodalEmbedder is an OPTIONAL capability interface for embedding models
// that accept image input, kept separate from Embedder so providers that do
// not implement it stay untouched.
type MultimodalEmbedder interface {
	// BatchEmbedMultimodal encodes a batch of inputs. The returned slice is
	// positionally aligned with inputs.
	BatchEmbedMultimodal(ctx context.Context, inputs []Input) ([][]float32, error)

	// Capabilities declares which modalities the model accepts and whether
	// they share a single vector space.
	Capabilities() Capabilities
}

// MultimodalEmbedderOf reports whether e exposes the multimodal capability.
// A true result means "the interface is present", not "images are supported" —
// use SupportsImage for an actual capability check.
func MultimodalEmbedderOf(e Embedder) (MultimodalEmbedder, bool) {
	mm, ok := e.(MultimodalEmbedder)
	return mm, ok
}

// ExtraConfigSupportsImage declares image-input support explicitly, overriding
// the model-name heuristic — needed for self-hosted checkpoints whose name
// carries no hint.
const ExtraConfigSupportsImage = "supports_image_embedding"

// LooksMultimodalModel reports whether a model name suggests image-input
// support. Keep the hints tied to families that are ONLY ever served as vision
// encoders: a false positive silently degrades a KB to OCR-only, a false
// negative costs the operator one extra_config line.
func LooksMultimodalModel(modelName string) bool {
	name := strings.ToLower(modelName)
	for _, hint := range []string{
		"vision", "multimodal", "clip", "siglip", "colpali", "colqwen", "wemm",
		"vl-embedding", "embed-vl", "vlm2vec",
	} {
		if strings.Contains(name, hint) {
			return true
		}
	}
	return false
}

// extraConfigBool reads a boolean-ish ExtraConfig entry. Returns nil when the
// key is absent or the value is not recognisable, so callers can fall back.
func extraConfigBool(extra map[string]string, key string) *bool {
	raw, ok := extra[key]
	if !ok {
		return nil
	}
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "true", "1", "yes", "on":
		v := true
		return &v
	case "false", "0", "no", "off":
		v := false
		return &v
	}
	return nil
}

// ResolveMultimodal decides whether a model is image-capable: an explicit
// ExtraConfig declaration wins, otherwise the model name decides.
func ResolveMultimodal(modelName string, extra map[string]string) bool {
	if declared := extraConfigBool(extra, ExtraConfigSupportsImage); declared != nil {
		return *declared
	}
	return LooksMultimodalModel(modelName)
}

// ExtraConfigMultimodalEnvelope picks the wire envelope a self-hosted
// multimodal embedding server speaks. The default (chat) is vLLM's `messages`
// envelope; SGLang needs the other one.
const ExtraConfigMultimodalEnvelope = "multimodal_envelope"

// MultimodalEnvelopeStyle selects the request shape used for multimodal
// embedding calls. The styles are NOT interchangeable — they produce different
// vectors for the same input — so it is resolved once per embedder and
// indexing and querying cannot drift apart.
type MultimodalEnvelopeStyle string

const (
	// EnvelopeChat is vLLM's `messages` envelope: content parts under
	// `messages[].content[]`, images as `image_url`. It carries a chat
	// template, which is why text has to travel through it when images do.
	EnvelopeChat MultimodalEnvelopeStyle = "chat"

	// EnvelopeSGLang is SGLang's flat `input: [{"text": …}, {"image": …}]`
	// array of typed items. There is no chat template and no way to fold text
	// and image into one vector, so it accepts text-only or a single image.
	EnvelopeSGLang MultimodalEnvelopeStyle = "sglang"
)

// ResolveMultimodalEnvelope picks the envelope style for a model. An
// unrecognised value falls back to chat rather than failing: the key is
// operator-supplied free text, and a typo that disables a model outright would
// be worse than one that keeps the old behaviour.
func ResolveMultimodalEnvelope(extra map[string]string) MultimodalEnvelopeStyle {
	raw, ok := extra[ExtraConfigMultimodalEnvelope]
	if !ok {
		return EnvelopeChat
	}
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case string(EnvelopeSGLang), "sgl":
		return EnvelopeSGLang
	default:
		return EnvelopeChat
	}
}

// SupportsImageForModel answers whether a stored embedding model row can
// encode images, without constructing the embedder. It is the single answer to
// "can this model see images?", so the UI cannot offer a switch that does
// nothing at ingestion time. False for nil or non-embedding models.
func SupportsImageForModel(m *types.Model) bool {
	if m == nil || m.Type != types.ModelTypeEmbedding {
		return false
	}
	return ResolveMultimodal(m.Name, m.Parameters.ExtraConfig)
}

// CapabilitiesFor builds the capability set for a model whose image support
// has already been resolved, so "what does image support mean" has one
// implementation. UnifiedSpace is assumed true: every target model
// (CLIP/SigLIP, WeMM, Qwen3-VL-Embedding, GME, ColPali) is contrastive.
func CapabilitiesFor(multimodal bool) Capabilities {
	if !multimodal {
		return Capabilities{Modalities: []Modality{ModalityText}}
	}
	return Capabilities{
		Modalities:   []Modality{ModalityText, ModalityImage},
		UnifiedSpace: true,
	}
}

// CapabilitiesOf returns the declared capabilities of e, falling back to a
// text-only set when e does not implement MultimodalEmbedder. Decorators use
// this rather than a hardcoded struct so Capabilities stays truthful through
// the wrappers NewEmbedder installs.
func CapabilitiesOf(e Embedder) Capabilities {
	if mm, ok := MultimodalEmbedderOf(e); ok {
		return mm.Capabilities()
	}
	return Capabilities{Modalities: []Modality{ModalityText}}
}

// SupportsImage reports whether e can embed images. Prefer it over a bare
// MultimodalEmbedderOf assertion: decorators always expose the interface but
// report the REAL capabilities of what they wrap, so a decorated text-only
// model still answers false.
func SupportsImage(e Embedder) bool {
	mm, ok := MultimodalEmbedderOf(e)
	return ok && mm.Capabilities().Supports(ModalityImage)
}

// BatchEmbedMultimodalWith forwards a multimodal batch to e, turning a missing
// capability into ErrMultimodalUnsupported instead of a nil dereference.
func BatchEmbedMultimodalWith(ctx context.Context, e Embedder, inputs []Input) ([][]float32, error) {
	mm, ok := MultimodalEmbedderOf(e)
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrMultimodalUnsupported, e.GetModelName())
	}
	return mm.BatchEmbedMultimodal(ctx, inputs)
}

// EmbedMultimodal encodes a single input with e, requiring image support. It
// is the convenience wrapper for the "one image in, one vector out" case.
func EmbedMultimodal(ctx context.Context, e Embedder, in Input) ([]float32, error) {
	if !SupportsImage(e) {
		return nil, fmt.Errorf("%w: %s", ErrMultimodalUnsupported, e.GetModelName())
	}
	if err := in.Validate(); err != nil {
		return nil, err
	}
	vectors, err := BatchEmbedMultimodalWith(ctx, e, []Input{in})
	if err != nil {
		return nil, err
	}
	if len(vectors) == 0 {
		return nil, errors.New("no embedding returned for multimodal input")
	}
	return vectors[0], nil
}
