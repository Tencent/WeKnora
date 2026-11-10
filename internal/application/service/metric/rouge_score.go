package metric

/*
# -*- coding: utf-8 -*-
# Copyright 2017 Google Inc.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#      http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

"""ROUGE Metric Implementation

This is a very slightly version of:
https://github.com/pltrdy/seq2seq/blob/master/seq2seq/metrics/rouge.py

---

ROUGe metric implementation.

This is a modified and slightly extended verison of
https://github.com/miso-belica/sumy/blob/dev/sumy/evaluation/rouge.py.
*/

import (
	"strings"
)

type Ngrams struct {
	ngrams    map[string]int
	exclusive bool
}

func NewNgrams(exclusive bool) *Ngrams {
	return &Ngrams{ngrams: make(map[string]int), exclusive: exclusive}
}

func (n *Ngrams) Add(o string) {
	if n.exclusive {
		n.ngrams[o] = 1
	} else {
		n.ngrams[o]++
	}
}

func (n *Ngrams) Len() int {
	return len(n.ngrams)
}

func (n *Ngrams) Intersection(o *Ngrams) *Ngrams {
	intersection := NewNgrams(n.exclusive)
	for k := range n.ngrams {
		if _, ok := o.ngrams[k]; ok {
			intersection.Add(k)
		}
	}
	return intersection
}

func (n *Ngrams) BatchAdd(o []string) {
	for _, v := range o {
		n.Add(v)
	}
}

func (n *Ngrams) Union(others ...*Ngrams) *Ngrams {
	union := NewNgrams(n.exclusive)
	for k := range n.ngrams {
		union.Add(k)
	}
	for _, other := range others {
		for k := range other.ngrams {
			union.Add(k)
		}
	}
	return union
}

func getNgrams(n int, text []string, exclusive bool) *Ngrams {
	ngramSet := NewNgrams(exclusive)
	for i := 0; i <= len(text)-n; i++ {
		ngramSet.Add(strings.Join(text[i:i+n], " "))
	}
	return ngramSet
}

func getWordNgrams(n int, sentences []string, exclusive bool) *Ngrams {
	words := splitIntoWords(sentences)
	return getNgrams(n, words, exclusive)
}

func lcs(x, y []string) [][]int {
	n, m := len(x), len(y)
	table := make([][]int, n+1)
	for i := range table {
		table[i] = make([]int, m+1)
	}
	for i := 1; i <= n; i++ {
		for j := 1; j <= m; j++ {
			if x[i-1] == y[j-1] {
				table[i][j] = table[i-1][j-1] + 1
			} else {
				table[i][j] = max(table[i-1][j], table[i][j-1])
			}
		}
	}
	return table
}

func reconLcs(x, y []string, exclusive bool) *Ngrams {
	i, j := len(x), len(y)
	table := lcs(x, y)

	var reconFunc func(int, int) []string
	reconFunc = func(i, j int) []string {
		if i == 0 || j == 0 {
			return []string{}
		} else if x[i-1] == y[j-1] {
			return append(reconFunc(i-1, j-1), x[i-1])
		} else if table[i-1][j] > table[i][j-1] {
			return reconFunc(i-1, j)
		} else {
			return reconFunc(i, j-1)
		}
	}

	reconList := reconFunc(i, j)
	ngramList := NewNgrams(exclusive)
	for _, word := range reconList {
		ngramList.Add(word)
	}
	return ngramList
}

func rougeN(evaluatedSentences, referenceSentences []string, n int, rawResults, exclusive bool) map[string]float64 {
	evaluatedNgrams := getWordNgrams(n, evaluatedSentences, exclusive)
	referenceNgrams := getWordNgrams(n, referenceSentences, exclusive)
	referenceCount := referenceNgrams.Len()
	evaluatedCount := evaluatedNgrams.Len()

	overlappingNgrams := evaluatedNgrams.Intersection(referenceNgrams)
	overlappingCount := overlappingNgrams.Len()

	results := make(map[string]float64)
	if rawResults {
		results["hyp"] = float64(evaluatedCount)
		results["ref"] = float64(referenceCount)
		results["overlap"] = float64(overlappingCount)
		return results
	} else {
		return calculateRougeN(evaluatedCount, referenceCount, overlappingCount)
	}
}

func calculateRougeN(evaluatedCount, referenceCount, overlappingCount int) map[string]float64 {
	results := make(map[string]float64)
	if evaluatedCount == 0 {
		results["p"] = 0.0
	} else {
		results["p"] = float64(overlappingCount) / float64(evaluatedCount)
	}

	if referenceCount == 0 {
		results["r"] = 0.0
	} else {
		results["r"] = float64(overlappingCount) / float64(referenceCount)
	}

	results["f"] = 2.0 * ((results["p"] * results["r"]) / (results["p"] + results["r"] + 1e-8))
	return results
}

func unionLcs(evaluatedSentences []string, referenceSentence string, prevUnion *Ngrams, exclusive bool) (int, *Ngrams) {
	if prevUnion == nil {
		prevUnion = NewNgrams(exclusive)
	}

	lcsUnion := prevUnion
	prevCount := len(prevUnion.ngrams)
	referenceWords := splitIntoWords([]string{referenceSentence})

	combinedLcsLength := 0
	for _, evalS := range evaluatedSentences {
		evaluatedWords := splitIntoWords([]string{evalS})
		lcs := reconLcs(referenceWords, evaluatedWords, exclusive)
		combinedLcsLength += lcs.Len()
		lcsUnion = lcsUnion.Union(lcs)
	}

	newLcsCount := lcsUnion.Len() - prevCount
	return newLcsCount, lcsUnion
}

func rougeLSummaryLevel(
	evaluatedSentences, referenceSentences []string,
	rawResults, exclusive bool,
) map[string]float64 {
	referenceNgrams := NewNgrams(exclusive)
	referenceNgrams.BatchAdd(splitIntoWords(referenceSentences))
	m := referenceNgrams.Len()

	evaluatedNgrams := NewNgrams(exclusive)
	evaluatedNgrams.BatchAdd(splitIntoWords(evaluatedSentences))
	n := evaluatedNgrams.Len()

	unionLcsSumAcrossAllReferences := 0
	union := NewNgrams(exclusive)
	for _, refS := range referenceSentences {
		lcsCount, newUnion := unionLcs(evaluatedSentences, refS, union, exclusive)
		union = newUnion
		unionLcsSumAcrossAllReferences += lcsCount
	}

	llcs := unionLcsSumAcrossAllReferences

	var rLcs float64
	if m == 0 {
		rLcs = 0.0
	} else {
		rLcs = float64(llcs) / float64(m)
	}
	var pLcs float64
	if n == 0 {
		pLcs = 0.0
	} else {
		pLcs = float64(llcs) / float64(n)
	}

	fLcs := 2.0 * ((pLcs * rLcs) / (pLcs + rLcs + 1e-8))

	results := make(map[string]float64)
	if rawResults {
		results["hyp"] = float64(n)
		results["ref"] = float64(m)
		results["overlap"] = float64(llcs)
		return results
	} else {
		results["f"] = fLcs
		results["p"] = pLcs
		results["r"] = rLcs
		return results
	}
}
