package livecache

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func outcomes(r *Result) (string, string) {
	if len(r.Steps) == 0 {
		return "not-executed", "not-executed"
	}
	cold, warm := 0, 0
	vectors := map[int]string{}
	same := true
	embeddingSteps := 0
	chatSteps := 0
	stable, control := 0, 0
	known := true
	facts := true
	for _, s := range r.Steps {
		if s.Operation == "embedding" {
			embeddingSteps++
			if s.Round == 1 {
				cold += s.ProviderRequests
				vectors[s.Pair] = s.VectorSHA256
			} else {
				warm += s.ProviderRequests
				if s.VectorSHA256 == "" || s.VectorSHA256 != vectors[s.Pair] {
					same = false
				}
			}
			continue
		}
		chatSteps++
		if s.CacheReadTokens == nil {
			known = false
		} else if s.Arm == "stable-prefix" {
			stable += *s.CacheReadTokens
		} else {
			control += *s.CacheReadTokens
		}
		if s.RequiredFactsPresent == nil || !*s.RequiredFactsPresent {
			facts = false
		}
	}
	embedOutcome := "embedding_comparison_incomplete"
	if embeddingSteps == 4 && same && cold == 2 && warm == 0 {
		embedOutcome = "physical_calls_reduced"
	} else if embeddingSteps == 4 {
		embedOutcome = "embedding_cache_contract_failed"
	}
	if chatSteps != MaxChatRequests || !known {
		return embedOutcome, "cache_comparison_incomplete"
	}
	if !facts {
		return embedOutcome, "required_fact_check_failed"
	}
	if stable+control == 0 {
		return embedOutcome, "no_cache_hits_observed"
	}
	if stable <= control {
		return embedOutcome, "no_stable_prefix_advantage"
	}
	return embedOutcome, "observed_stable_prefix_advantage"
}

func saveReport(dir string, p *Plan, r *Result) error {
	if err := writeJSON(filepath.Join(dir, "result.json"), r); err != nil {
		return err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# X08 controlled cache experiment\n\nMode: `%s`. Status: `%s`. Plan SHA-256: "+
		"`%s`.\n\n", r.Mode, r.Status, p.SHA256)
	fmt.Fprintf(&b, "The fixed plan permits at most %d chat requests and %d embedding requests, %d "+
		"input tokens under the UTF-8 byte envelope, and %d output tokens. Each chat "+
		"response is capped at %d tokens with thinking disabled. Original-price "+
		"reservation ceiling: CNY %.6f.\n\n",
		p.ChatRequestLimit, p.EmbeddingRequestLimit, p.InputTokenUpperBound, p.OutputTokenUpperBound,
		MaxOutputTokens, float64(p.OriginalPriceUpperBoundMicrounits)/1000000)
	fmt.Fprintf(&b, "Shared-context estimate: %d tokens using %s. %s First and repeated Wiki "+
		"observations do not certify a cold provider cache. Both arms use implicit "+
		"caching; this is an ordering experiment, not an on/off cache test.\n\n",
		p.SharedPrefixApproxTokens, p.PrefixEstimator, p.CacheEligibility)
	fmt.Fprintf(&b, "Observed outbound requests: chat %d, embedding %d. Reserved CNY %.6f. "+
		"Embedding result: `%s`. Wiki result: `%s`. Stop reason: `%s`.\n\n",
		r.ChatRequests, r.EmbeddingRequests, float64(r.ReservedMicrounits)/1000000,
		r.EmbeddingOutcome, r.WikiOutcome, r.StopReason)
	b.WriteString("The following rows show actual physical requests, ledger costs, cache reads " +
		"and elapsed time for each fixed step. Missing counters or costs are shown as " +
		"unknown. Application-cache hits incur zero new provider fees.\n\n| Step | " +
		"Requests | Ledger rows | Cost (CNY) | Cache read tokens | Time (ms) | Status " +
		"|\n| --- | ---: | ---: | ---: | ---: | ---: | --- |\n")
	for _, s := range r.Steps {
		cost, cached := "unknown", "unknown"
		if s.CostMicrounits != nil {
			cost = fmt.Sprintf("%.6f", float64(*s.CostMicrounits)/1000000)
		}
		if s.CacheReadTokens != nil {
			cached = fmt.Sprint(*s.CacheReadTokens)
		}
		fmt.Fprintf(&b, "| %s | %d | %d | %s | %s | %d | %s |\n",
			s.ID, s.ProviderRequests, s.LedgerRecords, cost, cached, s.DurationMs, s.Status)
	}
	b.WriteString("\nRead-token differences are descriptive observations from a small paired run. " +
		"Output checks only test the required seats, opening hours and SUMMARY prefix; " +
		"they do not establish semantic equivalence or absence of unsupported claims. " +
		"Full synthetic outputs and output hashes are saved in steps.json for review. A " +
		"miss, missing cache telemetry or failed call cannot establish a cache " +
		"benefit.\n\n")
	b.WriteString("Prices use Beijing list rates: [Qwen3.7 " +
		"Flash](https://help.aliyun.com/zh/model-studio/qwen3-7-flash), [Qwen3.7 text " +
		"embedding](https://help.aliyun.com/zh/model-studio/qwen3-7-text-embedding). " +
		"[Implicit cache " +
		"documentation](https://help.aliyun.com/zh/model-studio/context-cache) states " +
		"that the 1024-token threshold gives eligibility and does not guarantee a " +
		"hit.\n")
	return os.WriteFile(filepath.Join(dir, "comparison.md"), []byte(b.String()), 0o600)
}
