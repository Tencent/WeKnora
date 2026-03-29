package agent

import "sync"

// engineRegistry stores active AgentEngine instances keyed by "sessionID:messageID".
// This allows the HTTP handler to look up a running engine and send user input
// when the agent is blocked on the ask_user tool.
var engineRegistry sync.Map

// StoreEngine registers an active engine so it can be resumed via the REST API.
func StoreEngine(sessionID, messageID string, engine *AgentEngine) {
	engineRegistry.Store(sessionID+":"+messageID, engine)
}

// GetEngine retrieves an active engine by session and message ID.
func GetEngine(sessionID, messageID string) (*AgentEngine, bool) {
	v, ok := engineRegistry.Load(sessionID + ":" + messageID)
	if !ok {
		return nil, false
	}
	return v.(*AgentEngine), true
}

// RemoveEngine removes an engine from the registry after execution completes.
func RemoveEngine(sessionID, messageID string) {
	engineRegistry.Delete(sessionID + ":" + messageID)
}
