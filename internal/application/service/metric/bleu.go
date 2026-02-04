package metric

// references: https://github.com/waygo/bleu

// Package bleu implements the BLEU method, which is used to evaluate
// the quality of machine translation. [1]
//
// The code in this package was largely ported from the corresponding package
// in Python NLTK. [2]
//
// [1] Papineni, Kishore, et al. "BLEU: a method for automatic evaluation of
//     machine translation." Proceedings of the 40th annual meeting on
//     association for computational linguistics. Association for Computational
//     Linguistics, 2002.
//
// [2] http://www.nltk.org/_modules/nltk/align/bleu.html

import (
	"encoding/json"
	"log"
	"math"
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
)

type BLEUMetric struct {
	smoothing bool
	weights   BLEUWeight
}

func NewBLEUMetric(smoothing bool, weights BLEUWeight) *BLEUMetric {
	return &BLEUMetric{smoothing: smoothing, weights: weights}
}

type Sentence []string

type BLEUWeight []float64

var (
	BLEU1Gram BLEUWeight = []float64{1.0, 0.0, 0.0, 0.0}
	BLEU2Gram BLEUWeight = []float64{0.5, 0.5, 0.0, 0.0}
	BLEU3Gram BLEUWeight = []float64{0.33, 0.33, 0.33, 0.0}
	BLEU4Gram BLEUWeight = []float64{0.25, 0.25, 0.25, 0.25}
)

func (b *BLEUMetric) Compute(metricInput *types.MetricInput) float64 {
	candidate := splitIntoWords(splitSentences(metricInput.GeneratedTexts))
	references := []Sentence{splitIntoWords(splitSentences(metricInput.GeneratedGT))}

	for i := range candidate {
		candidate[i] = strings.ToLower(candidate[i])
	}

	for i := range references {
		for u := range references[i] {
			references[i][u] = strings.ToLower(references[i][u])
		}
	}

	ps := make([]float64, len(b.weights))
	for i := range b.weights {
		ps[i] = b.modifiedPrecision(candidate, references, i+1)
	}

	s := 0.0
	overlap := 0
	for i := range b.weights {
		w := b.weights[i]
		pn := ps[i]
		if pn > 0.0 {
			overlap++
			s += w * math.Log(pn)
		}
	}

	if overlap == 0 {
		return 0
	}

	bp := b.brevityPenalty(candidate, references)
	return bp * math.Exp(s)
}

type phrase []string

func (p phrase) String() string {
	b, err := json.Marshal(p)
	if err != nil {
		// Fallback to a simple string representation if JSON marshaling fails
		// This should never happen for a []string, but we handle it gracefully
		return strings.Join(p, " ")
	}
	return string(b)
}

func (b *BLEUMetric) getNphrase(s Sentence, n int) []phrase {
	nphrase := []phrase{}
	for i := 0; i < len(s)-n+1; i++ {
		nphrase = append(nphrase, phrase(s[i:i+n]))
	}
	return nphrase
}

func (b *BLEUMetric) countNphrase(nphrase []phrase) map[string]int {
	counts := map[string]int{}
	for _, gram := range nphrase {
		counts[gram.String()]++
	}
	return counts
}

func (b *BLEUMetric) modifiedPrecision(candidate Sentence, references []Sentence, n int) float64 {
	nphrase := b.getNphrase(candidate, n)
	if len(nphrase) == 0 {
		return 0.0
	}

	counts := b.countNphrase(nphrase)

	if len(counts) == 0 {
		return 0.0
	}

	maxCounts := map[string]int{}
	for i := range references {
		referenceCounts := b.countNphrase(b.getNphrase(references[i], n))
		for ngram := range counts {
			if v, ok := maxCounts[ngram]; !ok {
				maxCounts[ngram] = referenceCounts[ngram]
			} else if v < referenceCounts[ngram] {
				maxCounts[ngram] = referenceCounts[ngram]
			}
		}
	}

	clippedCounts := map[string]int{}
	for ngram, count := range counts {
		clippedCounts[ngram] = min(count, maxCounts[ngram])
	}

	smoothingFactor := 0.0
	if b.smoothing {
		smoothingFactor = 1.0
	}
	return (float64(sum(clippedCounts)) + smoothingFactor) / (float64(sum(counts)) + smoothingFactor)
}

func (b *BLEUMetric) brevityPenalty(candidate Sentence, references []Sentence) float64 {
	c := len(candidate)
	refLens := []int{}
	for i := range references {
		refLens = append(refLens, len(references[i]))
	}
	minDiffInd, minDiff := 0, -1
	for i := range refLens {
		if minDiff == -1 || abs(refLens[i]-c) < minDiff {
			minDiffInd = i
			minDiff = abs(refLens[i] - c)
		}
	}
	r := refLens[minDiffInd]
	if c > r {
		return 1
	}
	return math.Exp(float64(1 - float64(r)/float64(c)))
}
