package reranking

import (
	"context"
	"encoding/json"
	"sort"
	"sync"
	"unicode/utf8"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/models/imageprep"
	"github.com/Tencent/WeKnora/internal/models/rerank"
	"github.com/Tencent/WeKnora/internal/types"
	"golang.org/x/sync/errgroup"
)

// An image found by its own vector is judged by what it shows. A reranker
// that reads images is sent the image. A text reranker only sees the caption
// and OCR text, which speak for the image when the image is mostly text, but
// not when it is a photo, chart or diagram the caption can only summarize:
// there its rejection means "the caption does not say", not "irrelevant".
const (
	// pictorialOCRRunes is the OCR length below which an image counts as
	// pictorial: its words are labels, not its content. Empirical.
	pictorialOCRRunes = 200
	// maxKeptImages caps the pictorial image hits kept over a text
	// reranker's rejection, so an image-heavy knowledge base cannot fill a
	// context with images the vector merely brushed.
	maxKeptImages = 2
	// imageLoadConcurrency bounds image reads from storage.
	imageLoadConcurrency = 4
	// MinImageKeepScore is the least vector similarity that keeps a pictorial
	// image hit over a text reranker. It sits above the 0.1 image recall
	// threshold: recall only lets an image compete, keeping it needs more.
	// Empirical, on the cosine scale most engines report.
	MinImageKeepScore = 0.25
)

// ImageKeepScoreFor is Options.ImageKeepScore for a search whose text hits
// needed vectorThreshold: an image is kept over a text reranker only when its
// vector was at least as close as a text hit had to be, and never below
// MinImageKeepScore.
func ImageKeepScoreFor(vectorThreshold float64) float64 {
	return max(vectorThreshold, MinImageKeepScore)
}

func isImageVectorHit(r *types.SearchResult) bool {
	return r != nil && r.ChunkType == string(types.ChunkTypeImageVector)
}

// isPictorial reports whether an image hit's text falls short of its content:
// its images carry less OCR text than pictorialOCRRunes.
func isPictorial(ctx context.Context, r *types.SearchResult) bool {
	var infos []types.ImageInfo
	if err := json.Unmarshal([]byte(r.ImageInfo), &infos); err != nil || len(infos) == 0 {
		logger.Warnf(ctx, "[Rerank] Unreadable image info on image hit %s; treating it as text", r.ID)
		return false
	}
	runes := 0
	for _, info := range infos {
		runes += utf8.RuneCountInString(info.OCRText)
	}
	return runes < pictorialOCRRunes
}

// imageCandidates prepares the images of the image hits among candidates for
// a reranker that reads images. It returns the candidate positions scored as
// images and their images, in matching order; a hit whose image cannot be
// read or made to fit stays a text candidate.
func imageCandidates(
	ctx context.Context, model rerank.Reranker, candidates []*types.SearchResult, opts Options,
) (positions []int, images []rerank.Image) {
	imageModel, ok := rerank.AsImageReranker(model)
	if !ok || opts.LoadImage == nil {
		return nil, nil
	}
	limits := imageModel.ImageLimits()
	prepared := make([]*rerank.Image, len(candidates))
	var mu sync.Mutex
	group, groupCtx := errgroup.WithContext(ctx)
	group.SetLimit(imageLoadConcurrency)
	for i, r := range candidates {
		if !isImageVectorHit(r) {
			continue
		}
		group.Go(func() error {
			data, err := opts.LoadImage(groupCtx, r)
			if err != nil {
				logger.Warnf(groupCtx, "[Rerank] Image of %s unreadable, scoring its text: %v", r.ID, err)
				return nil
			}
			img, err := imageprep.Prepare(data, limits)
			if err != nil {
				logger.Warnf(groupCtx, "[Rerank] Image of %s does not fit the model, scoring its text: %v", r.ID, err)
				return nil
			}
			mu.Lock()
			prepared[i] = &img
			mu.Unlock()
			return nil
		})
	}
	_ = group.Wait()
	for i, img := range prepared {
		if img != nil {
			positions = append(positions, i)
			images = append(images, *img)
		}
	}
	return positions, images
}

// scoreCandidates scores every candidate: the ones at imagePositions by their
// image, the rest by their passage. Indices of the returned scores refer to
// candidates. If the images cannot be scored they are scored as text, so an
// image call failing costs the images their advantage, not the whole rerank.
func scoreCandidates(
	ctx context.Context, model rerank.Reranker, query string, passages []string,
	imagePositions []int, images []rerank.Image,
) (scores []rerank.RankResult, scoredAsImage map[int]bool, err error) {
	scoredAsImage = make(map[int]bool, len(imagePositions))
	if len(images) > 0 {
		imageModel, _ := rerank.AsImageReranker(model)
		imageScores, imgErr := imageModel.RerankImages(ctx, query, images)
		if imgErr != nil {
			logger.Warnf(ctx, "[Rerank] Image scoring failed, scoring those hits as text: %v", imgErr)
		} else {
			for _, s := range imageScores {
				if s.Index < 0 || s.Index >= len(imagePositions) {
					continue
				}
				s.Index = imagePositions[s.Index]
				scoredAsImage[s.Index] = true
				scores = append(scores, s)
			}
		}
	}
	textPositions := make([]int, 0, len(passages))
	textPassages := make([]string, 0, len(passages))
	for i, p := range passages {
		if !scoredAsImage[i] {
			textPositions = append(textPositions, i)
			textPassages = append(textPassages, p)
		}
	}
	if len(textPassages) == 0 {
		return scores, scoredAsImage, nil
	}
	textScores, err := model.Rerank(ctx, query, textPassages)
	if err != nil {
		return nil, nil, err
	}
	for _, s := range textScores {
		if s.Index < 0 || s.Index >= len(textPositions) {
			continue
		}
		s.Index = textPositions[s.Index]
		scores = append(scores, s)
	}
	sort.SliceStable(scores, func(i, j int) bool { return scores[i].RelevanceScore > scores[j].RelevanceScore })
	return scores, scoredAsImage, nil
}

// keptImages picks the pictorial image hits a text reranker rejected that
// their vector still vouches for: VectorScore at least opts.ImageKeepScore,
// strongest first, at most maxKeptImages. Hits scored by their image already
// had a fair judgement and are never kept over it.
func keptImages(
	ctx context.Context, candidates []*types.SearchResult, scores, passing []rerank.RankResult,
	scoredAsImage map[int]bool, opts Options,
) []rerank.RankResult {
	if opts.ImageKeepScore <= 0 {
		return nil
	}
	passed := make(map[int]bool, len(passing))
	for _, p := range passing {
		passed[p.Index] = true
	}
	var kept []rerank.RankResult
	for _, s := range scores {
		r := candidates[s.Index]
		if passed[s.Index] || scoredAsImage[s.Index] || !isImageVectorHit(r) ||
			r.VectorScore < opts.ImageKeepScore || !isPictorial(ctx, r) {
			continue
		}
		kept = append(kept, s)
	}
	sort.SliceStable(kept, func(i, j int) bool {
		return candidates[kept[i].Index].VectorScore > candidates[kept[j].Index].VectorScore
	})
	if len(kept) > maxKeptImages {
		kept = kept[:maxKeptImages]
	}
	return kept
}
