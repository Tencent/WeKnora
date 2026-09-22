// Package contextusage attributes one LLM request's prompt tokens to the kind
// of content that held them.
//
// The hard constraint is that the buckets reconcile with the provider's
// prompt_tokens. Scaling every bucket to reach that sum is the obvious way
// there and it is wrong, because the estimation error is not evenly spread: a
// cl100k estimate is close for an English-and-JSON system prompt and tool
// schemas, and far off for Chinese conversation and tool output — 16-50% on
// short text and near 88% on long Chinese documents. Spreading that error
// proportionally makes the system prompt, frozen for the whole turn, climb
// every round as the conversation grows.
//
// So the fixed prefix is anchored and the residual goes to the variable
// buckets, which is where the error came from.
package contextusage

import (
	"strings"

	"github.com/Tencent/WeKnora/internal/agent/token"
	agenttools "github.com/Tencent/WeKnora/internal/agent/tools"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
)

// Section names of the rendered system prompt that get their own bucket.
const (
	SectionMemory = "memory"
	SectionSkills = "skills"
)

const (
	// minCalibrationDelta is how much the conversation has to grow between two
	// rounds before the growth is worth measuring. A small denominator turns
	// rounding noise into a scale factor.
	minCalibrationDelta = 500

	minVarScale   = 0.3
	maxVarScale   = 2.0
	minFixedScale = 0.3
	maxFixedScale = 1.5
)

// fixed is the part of a request that does not change from round to round.
type fixed struct {
	SystemPrompt int
	Memory       int
	Skills       int
	Tools        int
	MCP          int
}

func (f fixed) sum() int {
	return f.SystemPrompt + f.Memory + f.Skills + f.Tools + f.MCP
}

func (f fixed) scaled(k float64) fixed {
	return fixed{
		SystemPrompt: scale(f.SystemPrompt, k),
		Memory:       scale(f.Memory, k),
		Skills:       scale(f.Skills, k),
		Tools:        scale(f.Tools, k),
		MCP:          scale(f.MCP, k),
	}
}

// variable is the part that grows every round and carries the tokenizer error.
type variable struct {
	Conversation int
	Reasoning    int
	ToolResults  int
}

func (v variable) sum() int { return v.Conversation + v.Reasoning + v.ToolResults }

// Attributor holds one turn's locked buckets and calibration. The engine is
// stateless across turns, so a fresh one per turn is the intended lifetime.
type Attributor struct {
	est       *token.Estimator
	window    int
	threshold int

	lastFixed fixed
	lastVar   variable

	hasLocked   bool
	lockedEst   fixed
	lockedFixed fixed

	kFixed     float64
	calibrated bool
	prevPrompt int
	prevVarEst int
}

func New(est *token.Estimator, window, threshold int) *Attributor {
	if window <= 0 {
		window = types.DefaultMaxContextTokens
	}
	return &Attributor{est: est, window: window, threshold: threshold, kFixed: 1}
}

// Attribute estimates one request and reports it. promptTokens <= 0 means the
// provider has not priced the request yet, and the report says so.
func (a *Attributor) Attribute(
	messages []chat.Message,
	tools []chat.Tool,
	sectionTokens map[string]int,
	promptTokens int,
) types.ContextUsage {
	if a == nil || a.est == nil {
		return types.ContextUsage{Window: a.windowOrDefault(), Threshold: a.thresholdOrZero()}
	}
	a.lastFixed, a.lastVar = a.estimate(messages, tools, sectionTokens)
	return a.report(promptTokens)
}

// Recalibrate re-reports the request Attribute last saw, now that the provider
// has said what it cost. Reusing the estimate keeps the round to one walk of
// the history instead of two.
func (a *Attributor) Recalibrate(promptTokens int) types.ContextUsage {
	if a == nil || a.est == nil {
		return types.ContextUsage{Window: a.windowOrDefault(), Threshold: a.thresholdOrZero()}
	}
	return a.report(promptTokens)
}

func (a *Attributor) report(promptTokens int) types.ContextUsage {
	usage := types.ContextUsage{Window: a.window, Threshold: a.threshold}

	if promptTokens <= 0 {
		usage.Estimated = true
		setFixed(&usage, a.lastFixed)
		setVariable(&usage, a.lastVar)
		usage.Total = a.lastFixed.sum() + a.lastVar.sum()
		return usage
	}

	a.calibrate(promptTokens)
	f := a.resolveFixed()

	residual := promptTokens - f.sum()
	if residual < 0 {
		// The fixed estimate overshot the entire request. Report what fits and
		// drop the lock so the next round re-derives instead of compounding.
		f = shrinkTo(f, promptTokens)
		residual = 0
		a.hasLocked = false
		a.calibrated = false
		a.kFixed = 1
	}

	setFixed(&usage, f)
	setVariable(&usage, distribute(a.lastVar, residual))
	usage.Total = promptTokens

	a.prevPrompt = promptTokens
	a.prevVarEst = a.lastVar.sum()
	return usage
}

// resolveFixed reports the same numbers for the same estimate. Recomputing the
// system prompt every round is what used to make it drift.
func (a *Attributor) resolveFixed() fixed {
	if a.hasLocked && a.lockedEst == a.lastFixed {
		return a.lockedFixed
	}
	a.lockedEst = a.lastFixed
	a.lockedFixed = a.lastFixed.scaled(a.kFixed)
	a.hasLocked = true
	return a.lockedFixed
}

// calibrate measures how far the tokenizer is off on variable content, then
// reads the fixed prefix's true size off the same request. Two consecutive
// rounds share the prefix, so everything that grew between them is variable.
func (a *Attributor) calibrate(promptTokens int) {
	if a.calibrated || a.prevPrompt <= 0 {
		return
	}
	deltaEst := a.lastVar.sum() - a.prevVarEst
	deltaActual := promptTokens - a.prevPrompt
	if deltaEst < minCalibrationDelta || deltaActual <= 0 {
		return
	}
	kVar := clamp(float64(deltaActual)/float64(deltaEst), minVarScale, maxVarScale)
	fixedActual := float64(promptTokens) - kVar*float64(a.lastVar.sum())
	if fixedActual <= 0 || a.lastFixed.sum() <= 0 {
		return
	}
	a.kFixed = clamp(fixedActual/float64(a.lastFixed.sum()), minFixedScale, maxFixedScale)
	a.calibrated = true
	// Leave hasLocked alone: an unchanged fingerprint must keep the reported
	// fixed buckets. The new kFixed applies on the next fingerprint change.
}

func (a *Attributor) estimate(
	messages []chat.Message, tools []chat.Tool, sectionTokens map[string]int,
) (fixed, variable) {
	var f fixed
	var v variable

	var builtin, mcp []chat.Tool
	for _, tool := range tools {
		if IsMCPToolSchema(tool.Function.Name) {
			mcp = append(mcp, tool)
			continue
		}
		builtin = append(builtin, tool)
	}
	f.Tools = a.est.EstimateTools(builtin)
	f.MCP = a.est.EstimateTools(mcp)

	system := 0
	for i := range messages {
		msg := &messages[i]
		switch msg.Role {
		case "system":
			system += a.est.EstimateMessage(msg)
		case "tool":
			v.ToolResults += a.est.EstimateMessage(msg)
		default:
			parts := a.est.EstimateMessageParts(msg)
			v.Reasoning += parts.Reasoning
			v.Conversation += parts.Rest
		}
	}

	// Memory and skills are sections of the rendered system prompt, so they can
	// only be charged against what the system messages actually cost. Per-section
	// estimates do not add up to the rendered whole — sections are joined with
	// blank lines and BPE merges across the seams — and that difference stays
	// with the system prompt rather than being dropped.
	if system > 0 {
		f.Memory = min(sectionTokens[SectionMemory], system)
		f.Skills = min(sectionTokens[SectionSkills], system-f.Memory)
		f.SystemPrompt = system - f.Memory - f.Skills
	}

	if len(messages) > 0 {
		if tail := a.est.EstimateMessages(messages) - (system + v.sum()); tail > 0 {
			v.Conversation += tail
		}
	}
	return f, v
}

// distribute splits the residual across the variable buckets by estimated
// share. These three are homogeneous — the same prose and code under the same
// tokenizer bias — so a proportional split holds here, which is exactly what
// it fails to do across the fixed/variable boundary.
func distribute(est variable, residual int) variable {
	if residual <= 0 {
		return variable{}
	}
	total := est.sum()
	if total <= 0 {
		return variable{Conversation: residual}
	}
	out := variable{
		Conversation: est.Conversation * residual / total,
		Reasoning:    est.Reasoning * residual / total,
		ToolResults:  est.ToolResults * residual / total,
	}
	largest := &out.Conversation
	if est.Reasoning > est.Conversation && est.Reasoning >= est.ToolResults {
		largest = &out.Reasoning
	} else if est.ToolResults > est.Conversation && est.ToolResults > est.Reasoning {
		largest = &out.ToolResults
	}
	*largest += residual - out.sum()
	return out
}

func shrinkTo(f fixed, budget int) fixed {
	total := f.sum()
	if total <= 0 || budget <= 0 {
		return fixed{}
	}
	out := f.scaled(float64(budget) / float64(total))
	// Rounding can push the scaled sum past the budget. The system prompt is
	// often the bucket that should absorb that, and it is also often zero —
	// clamping a negative remainder there used to leave the buckets above Total.
	adjust(&out, budget-out.sum())
	return out
}

// adjust moves delta onto the buckets without going negative. A surplus lands
// on the largest bucket. A deficit is peeled from the largest positive buckets.
func adjust(f *fixed, delta int) {
	buckets := []*int{&f.SystemPrompt, &f.Memory, &f.Skills, &f.Tools, &f.MCP}
	for delta != 0 {
		var best *int
		for _, b := range buckets {
			if delta < 0 && *b <= 0 {
				continue
			}
			if best == nil || *b > *best {
				best = b
			}
		}
		if best == nil {
			return
		}
		if delta > 0 {
			*best += delta
			return
		}
		take := min(-delta, *best)
		*best -= take
		delta += take
	}
}

func setFixed(u *types.ContextUsage, f fixed) {
	u.SystemPrompt, u.Memory, u.Skills, u.Tools, u.MCP = f.SystemPrompt, f.Memory, f.Skills, f.Tools, f.MCP
}

func setVariable(u *types.ContextUsage, v variable) {
	u.Conversation, u.Reasoning, u.ToolResults = v.Conversation, v.Reasoning, v.ToolResults
}

func scale(v int, k float64) int {
	if v <= 0 {
		return 0
	}
	return int(float64(v)*k + 0.5)
}

func clamp(v, lo, hi float64) float64 { return min(max(v, lo), hi) }

func (a *Attributor) windowOrDefault() int {
	if a == nil || a.window <= 0 {
		return types.DefaultMaxContextTokens
	}
	return a.window
}

func (a *Attributor) thresholdOrZero() int {
	if a == nil {
		return 0
	}
	return a.threshold
}

// IsMCPToolSchema reports whether a tool definition came from MCP rather than
// the builtin set.
func IsMCPToolSchema(name string) bool {
	switch name {
	case agenttools.ToolDiscoverMCPTools, agenttools.ToolCallMCPTool:
		return true
	}
	return strings.HasPrefix(name, "mcp_")
}
