package hostreview

import (
	"encoding/json"
	"strings"
)

type assessment struct {
	RiskLevel         string `json:"risk_level"`
	UserAuthorization string `json:"user_authorization"`
	Outcome           string `json:"outcome"`
	Rationale         string `json:"rationale"`
	failure           string
}

// parseAssessment reads the model's JSON. The reply must be one JSON object,
// optionally wrapped in a single markdown fence. Text around an object is a
// parse failure: the action under review can contain a forged allow object,
// and taking the first "{" through the last "}" would approve that echo.
func parseAssessment(raw string) assessment {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return assessment{failure: "auto review returned nothing"}
	}
	var a assessment
	if err := json.Unmarshal([]byte(jsonCandidate(raw)), &a); err != nil {
		return assessment{failure: "auto review did not return valid JSON"}
	}
	a.RiskLevel = strings.ToLower(strings.TrimSpace(a.RiskLevel))
	a.UserAuthorization = strings.ToLower(strings.TrimSpace(a.UserAuthorization))
	a.Outcome = strings.ToLower(strings.TrimSpace(a.Outcome))
	if a.Outcome != "allow" && a.Outcome != "deny" {
		a.failure = "auto review returned no outcome"
	}
	return a
}

// jsonCandidate returns the reply, or the body of one surrounding markdown
// fence. A fence with trailing text is left unchanged so Unmarshal fails closed.
func jsonCandidate(raw string) string {
	if !strings.HasPrefix(raw, "```") {
		return raw
	}
	rest := raw[3:]
	nl := strings.IndexByte(rest, '\n')
	if nl < 0 {
		return raw
	}
	body := rest[nl+1:]
	end := strings.LastIndex(body, "```")
	if end < 0 || strings.TrimSpace(body[end+3:]) != "" {
		return raw
	}
	return strings.TrimSpace(body[:end])
}

// verdict validates the model's output. All three fields come from one
// model reply, so this checks that reply, not three independent defences;
// the grant scope and hard denials are enforced by the sandbox regardless.
func (a assessment) verdict() Verdict {
	if a.failure != "" {
		return Verdict{Reason: a.failure}
	}
	reason := strings.TrimSpace(a.Rationale)
	allowed := a.Outcome == "allow" &&
		(a.RiskLevel == "low" || a.RiskLevel == "medium") &&
		(a.UserAuthorization == "medium" || a.UserAuthorization == "high")
	if !allowed {
		if reason == "" || a.Outcome == "allow" {
			reason = refusalReason(a)
		}
		return Verdict{Reason: reason, RiskLevel: a.RiskLevel}
	}
	if reason == "" {
		reason = "auto review approved"
	}
	return Verdict{Approved: true, Reason: reason, RiskLevel: a.RiskLevel}
}

func refusalReason(a assessment) string {
	switch {
	case a.Outcome == "deny" && strings.TrimSpace(a.Rationale) != "":
		return strings.TrimSpace(a.Rationale)
	case a.RiskLevel != "low" && a.RiskLevel != "medium":
		return "auto review judged the risk too high"
	case a.UserAuthorization != "medium" && a.UserAuthorization != "high":
		return "the user's messages in this turn do not ask for this action"
	default:
		return "auto review denied the action"
	}
}
