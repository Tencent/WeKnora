package modelcontext

import "testing"

// Every key a tool declares as a source-ID argument must have a registration
// space, otherwise its values could be decoded from handles but never
// registered (or vice versa) — the drift this table exists to prevent.
func TestEverySourceIDKeyHasARegistrationSpace(t *testing.T) {
	for tool, policy := range toolHandlePolicies {
		for key := range policy.sourceIDKeys {
			if _, ok := sourceKeySpaces[key]; !ok {
				t.Errorf("tool %s declares source key %q with no entry in sourceKeySpaces", tool, key)
			}
		}
		for key := range policy.sourceTextKeys {
			if _, ok := sourceKeySpaces[key]; ok {
				t.Errorf("tool %s declares %q as free text but sourceKeySpaces also claims it as an ID key", tool, key)
			}
		}
	}
}
