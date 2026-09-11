package service

import (
	"context"
	"testing"
	"time"

	builtin "github.com/Tencent/WeKnora/internal/builtin/skills"
	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

type browserLifecycleManager struct {
	*capableManager
	running    bool
	provisions int
	liveCalls  int
}

func (m *browserLifecycleManager) ExecLiveSessionCommand(
	context.Context,
	string,
	string,
	time.Duration,
) (*sandbox.ExecuteResult, error) {
	m.liveCalls++
	if !m.running {
		return nil, sandbox.ErrSandboxPaused
	}
	return &sandbox.ExecuteResult{Stdout: `{"ok":true,"state":"running"}`}, nil
}

func (m *browserLifecycleManager) ExecShellCommand(
	_ context.Context,
	_, command, _ string,
	_ time.Duration,
	_ map[string]string,
) (*sandbox.ExecuteResult, error) {
	if command != "true" {
		panic("browser provisioning should only ensure the session")
	}
	m.provisions++
	m.running = true
	return &sandbox.ExecuteResult{}, nil
}

func TestBrowserStartResumesPinnedSandboxAndCreatesOnlyOnExplicitStart(t *testing.T) {
	for _, pinned := range []bool{false, true} {
		t.Run(map[bool]string{false: "new session", true: "paused session"}[pinned], func(t *testing.T) {
			ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7))
			pinner := NewSessionSandboxPinner(newPinTestDB(t))
			if pinned {
				_, err := pinner.Pin(ctx, "s-1", "office")
				require.NoError(t, err)
			}
			mgr := &browserLifecycleManager{capableManager: &capableManager{typ: sandbox.SandboxTypeCube}}
			resolver := &sessionSkillResolver{mgr: mgr}
			svc := NewSandboxTerminalService(pinner, resolver, nil, nil)
			_, err := svc.BrowserCommand(ctx, "s-1", BrowserCommand{Action: "frame"})
			require.Error(t, err)
			require.Zero(t, mgr.provisions, "opening preview must not create or resume")
			configID := "office"
			if pinned {
				configID = "other-agent-config"
			}
			result, err := svc.StartSessionBrowser(ctx, "s-1", configID, BrowserCommand{Action: "start"})
			require.NoError(t, err)
			require.JSONEq(t, `{"ok":true,"state":"running"}`, string(result))
			require.Equal(t, 1, mgr.provisions)
			require.Equal(t, "office", resolver.configID, "keep the existing session pin")
			_, err = svc.StartSessionBrowser(ctx, "s-1", configID, BrowserCommand{Action: "start"})
			require.NoError(t, err)
			require.Equal(t, 1, mgr.provisions, "already running browser must not reprovision")
		})
	}
}

func TestBrowserStartWithoutSandboxSelectionDoesNotProvision(t *testing.T) {
	svc := NewSandboxTerminalService(nil, nil, nil, nil)
	_, err := svc.StartSessionBrowser(context.Background(), "s-1", "", BrowserCommand{Action: "start"})
	require.ErrorIs(t, err, sandbox.ErrNoLiveSessionSandbox)
	_, err = svc.StartSessionBrowser(context.Background(), "s-1", "office", BrowserCommand{Action: "frame"})
	require.Error(t, err)
}

func TestBrowserPointerValidation(t *testing.T) {
	for _, phase := range []string{"down", "move", "up", "cancel"} {
		require.NoError(t, (BrowserCommand{Action: "pointer", Phase: phase, X: 100, Y: 200}).Validate())
	}
	require.Error(t, (BrowserCommand{Action: "pointer", Phase: "unknown"}).Validate())
	require.Error(t, (BrowserCommand{Action: "pointer", Phase: "move", X: 1281}).Validate())
	require.Error(t, (BrowserCommand{Action: "pointer", Phase: "move", Y: -1}).Validate())
}

type browserCapabilityManager struct {
	*browserLifecycleManager
	manifest  *types.BuiltinSkillsManifest
	sessionID string
	installed bool
}

func (m *browserCapabilityManager) BuiltinSkills(
	_ context.Context,
	sessionID string,
) (*types.BuiltinSkillsManifest, error) {
	m.sessionID = sessionID
	return m.manifest, nil
}

func (m *browserCapabilityManager) ExecLiveSessionCommand(
	context.Context,
	string,
	string,
	time.Duration,
) (*sandbox.ExecuteResult, error) {
	m.liveCalls++
	if !m.running {
		return nil, sandbox.ErrSandboxPaused
	}
	stdout := "no"
	if m.installed {
		stdout = "yes"
	}
	return &sandbox.ExecuteResult{Stdout: stdout}, nil
}

func TestBrowserCapabilityUsesLiveManifestAndNeverProvisions(t *testing.T) {
	office, err := builtin.PublishedManifestForProfile("office-core")
	require.NoError(t, err)
	legacyBrowserManifest := builtin.PublishedManifest()
	legacyBrowserManifest.Profile = "office-browser"
	for _, entry := range builtin.List() {
		if entry.Name == "browser" {
			legacyBrowserManifest.Skills = append(legacyBrowserManifest.Skills, types.BuiltinSkillDeclaration{
				Name: entry.Name, Digest: entry.Digest, Verified: true,
			})
		}
	}

	for _, tc := range []struct {
		name                     string
		manifest                 *types.BuiltinSkillsManifest
		running, installed, want bool
	}{
		{"office running", office, true, false, false},
		{"office paused", office, false, false, false},
		{"legacy browser paused", legacyBrowserManifest, false, false, true},
		{"custom controller", office, true, true, true},
		{"unknown image", nil, true, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mgr := &browserCapabilityManager{
				browserLifecycleManager: &browserLifecycleManager{
					capableManager: &capableManager{typ: sandbox.SandboxTypeDocker}, running: tc.running,
				},
				manifest: tc.manifest, installed: tc.installed,
			}
			available, err := sessionBrowserAvailable(context.Background(), mgr, "live-session", nil)
			require.NoError(t, err)
			require.Equal(t, tc.want, available)
			require.Equal(t, "live-session", mgr.sessionID,
				"must query the actual session rather than the default template")
			require.Zero(t, mgr.provisions)
		})
	}
}

func TestBrowserCapabilityKeepsPinnedConfigAndDoesNotPinNewSessions(t *testing.T) {
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7))
	pinner := NewSessionSandboxPinner(newPinTestDB(t))
	_, err := pinner.Pin(ctx, "s-1", "pinned-office")
	require.NoError(t, err)
	office, err := builtin.PublishedManifestForProfile("office-core")
	require.NoError(t, err)
	mgr := &browserCapabilityManager{browserLifecycleManager: &browserLifecycleManager{
		capableManager: &capableManager{typ: sandbox.SandboxTypeDocker},
	}, manifest: office}
	resolver := &sessionSkillResolver{mgr: mgr}
	svc := &sessionService{sandboxResolver: resolver, sandboxPinner: pinner}
	available, err := svc.SessionBrowserAvailable(ctx, 7, "s-1", "new-agent-config")
	require.NoError(t, err)
	require.False(t, available)
	require.Equal(t, "pinned-office", resolver.configID)
	_, err = svc.SessionBrowserAvailable(ctx, 7, "s-2", "new-agent-config")
	require.NoError(t, err)
	pin, err := pinner.Read(ctx, "s-2")
	require.NoError(t, err)
	require.Empty(t, pin)
	require.Zero(t, mgr.provisions)
}
