package localsandbox

import (
	"slices"
	"sync"
)

type sessionState struct {
	grants []Grant
	// rules are the delete rules approved for the session, by cwd.
	rules map[string][]DeleteRule
}

// sessionStates holds what the user approved in each session. It lives in
// memory only: after a restart the user is asked again, which is the
// conservative default for something that widens the sandbox.
type sessionStates struct {
	mu   sync.Mutex
	byID map[string]*sessionState
}

func newSessionStates() *sessionStates {
	return &sessionStates{byID: make(map[string]*sessionState)}
}

func (s *sessionStates) stateLocked(id string) *sessionState {
	st, ok := s.byID[id]
	if !ok {
		st = &sessionState{
			rules: make(map[string][]DeleteRule),
		}
		s.byID[id] = st
	}
	return st
}

func (s *sessionStates) grants(id string) []Grant {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.byID[id]
	if !ok {
		return nil
	}
	return append([]Grant(nil), st.grants...)
}

func (s *sessionStates) addGrant(id string, g Grant) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.stateLocked(id)
	for _, existing := range st.grants {
		if existing == g {
			return
		}
	}
	st.grants = append(st.grants, g)
}

// rulesCover reports whether every rule is covered by one approved in cwd.
// An empty list is not approved: there is nothing to skip an ask for.
func (s *sessionStates) rulesCover(id, cwd string, rules []DeleteRule) bool {
	if len(rules) == 0 {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.byID[id]
	if !ok {
		return false
	}
	approved := st.rules[cwd]
	for _, rule := range rules {
		if !slices.ContainsFunc(approved, func(a DeleteRule) bool { return a.Covers(rule) }) {
			return false
		}
	}
	return true
}

func (s *sessionStates) approveRule(id, cwd string, rule DeleteRule) {
	if rule.Program == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.stateLocked(id)
	if slices.ContainsFunc(st.rules[cwd], func(a DeleteRule) bool { return a.Covers(rule) }) {
		return
	}
	st.rules[cwd] = append(st.rules[cwd], rule)
}

func (s *sessionStates) release(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.byID, id)
}
