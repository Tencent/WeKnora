package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/Tencent/WeKnora/internal/types"
)

// fakeDesktopManager embeds sandbox.Manager so it satisfies the interface;
// only the methods the eligibility check calls are implemented.
type fakeDesktopManager struct {
	sandbox.Manager
	kind    sandbox.SandboxType
	desktop bool
	browser sandbox.SessionSecureBrowser
}

func (f *fakeDesktopManager) GetType() sandbox.SandboxType                  { return f.kind }
func (f *fakeDesktopManager) DesktopEnabledForPrompt() bool                 { return f.desktop }
func (f *fakeDesktopManager) SessionSecureBrowser() sandbox.SessionSecureBrowser { return f.browser }

type stubSecureBrowser struct{ sandbox.SessionSecureBrowser }

func webUserCtx() context.Context {
	return types.WithPrincipal(context.Background(), types.Principal{Type: types.PrincipalWebUser, ID: "u1"})
}

func TestSecureFormBrowserEligibility(t *testing.T) {
	browser := &stubSecureBrowser{}
	ready := &fakeDesktopManager{kind: sandbox.SandboxTypeCube, desktop: true, browser: browser}
	skills := &types.AgentConfig{SkillsEnabled: true}

	assert.Same(t, browser, secureFormBrowser(webUserCtx(), ready, skills))
	assert.Nil(t, secureFormBrowser(webUserCtx(), ready, &types.AgentConfig{}), "no sandbox shell entitlement")
	assert.Nil(t, secureFormBrowser(webUserCtx(), nil, skills))
	assert.Nil(t, secureFormBrowser(webUserCtx(),
		&fakeDesktopManager{kind: sandbox.SandboxTypeCube, desktop: false, browser: browser}, skills), "desktop off")
	assert.Nil(t, secureFormBrowser(webUserCtx(),
		&fakeDesktopManager{kind: sandbox.SandboxTypeHost, desktop: true, browser: browser}, skills), "Lite host sandbox")

	embed := types.WithPrincipal(context.Background(), types.Principal{Type: types.PrincipalEmbedVisitor, ID: "1:c:v"})
	assert.Nil(t, secureFormBrowser(embed, ready, skills), "embed visitors own no vault")
	automation := types.WithPrincipal(context.Background(),
		types.Principal{Type: types.PrincipalWebUser, ID: "u1", ActingVia: "automation:a1"})
	assert.Nil(t, secureFormBrowser(automation, ready, skills), "nobody can answer the card")
}
