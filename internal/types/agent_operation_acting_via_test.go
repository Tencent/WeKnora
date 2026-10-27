package types

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestActingViaFromContext(t *testing.T) {
	assert.Empty(t, ActingViaFromContext(context.Background()))

	ctx := WithPrincipal(context.Background(), Principal{
		Type: PrincipalWebUser, ID: "u1", ActingVia: " automation:a-1 ",
	})
	assert.Equal(t, "automation:a-1", ActingViaFromContext(ctx))
	p, _ := PrincipalFromContext(ctx)
	assert.Equal(t, "web_user:u1", p.StorageID(), "acting for a user keeps their identity")

	ctx = WithAuditAgentAction(ctx, AuditAgentAction{SessionID: "s", ActingVia: "automation:a-2"})
	assert.Equal(t, "automation:a-2", ActingViaFromContext(ctx), "the dispatched action's record wins")
}
