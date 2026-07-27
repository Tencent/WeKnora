package skillrunner

import (
	"strings"
	"testing"
)

func TestBuildContainerSpecEnforcesIsolation(t *testing.T) {
	request := ExecuteRequest{
		ExecutionID: "exec-1", TenantID: "10000", SkillID: "skill-id",
		VersionID: "version-id", ContentHash: strings.Repeat("a", 64), ScriptPath: "scripts/run.py", Args: []string{"--safe"},
	}
	args, err := BuildContainerSpec(request, ResolvedVersion{
		SourcePath: "/data/skills/10000/skill-id/version-id", ContentHash: strings.Repeat("a", 64), SourceVolume: "tenant-skills", AllowedScripts: []string{"scripts/run.py"},
	})
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(args, " ")
	for _, required := range []string{"--network=none", "--read-only", "--cap-drop=ALL", "no-new-privileges", "--user=1000:1000", "/skill:ro", "/work:rw,noexec,nosuid,nodev,size=64m"} {
		if !strings.Contains(joined, required) {
			t.Fatalf("missing %q in %s", required, joined)
		}
	}
	for _, forbidden := range []string{"--privileged", "--network=host", "/var/run/docker.sock"} {
		if strings.Contains(joined, forbidden) {
			t.Fatalf("forbidden %q in %s", forbidden, joined)
		}
	}
}

func TestValidateRequestRejectsCallerControlledOrUnsafeValues(t *testing.T) {
	tests := []ExecuteRequest{
		{ExecutionID: "exec", TenantID: "1", SkillID: "skill", VersionID: "version", ContentHash: strings.Repeat("a", 64), ScriptPath: "../run.py"},
		{ExecutionID: "exec", TenantID: "1", SkillID: "skill", VersionID: "version", ContentHash: strings.Repeat("a", 64), ScriptPath: "run.exe"},
		{ExecutionID: "exec", TenantID: "1", SkillID: "skill", VersionID: "version", ContentHash: strings.Repeat("a", 64), ScriptPath: "run.py", Args: make([]string, MaxArgs+1)},
	}
	for _, request := range tests {
		if err := ValidateRequest(request); err == nil {
			t.Fatalf("expected rejection for %+v", request)
		}
	}
}

func TestBuildContainerSpecUsesOnlyResolvedManifestScripts(t *testing.T) {
	request := ExecuteRequest{ExecutionID: "exec", TenantID: "1", SkillID: "skill", VersionID: "version", ContentHash: strings.Repeat("a", 64), ScriptPath: "scripts/hidden.py"}
	version := ResolvedVersion{SourcePath: "/data/skills/1/skill/version", ContentHash: request.ContentHash, SourceVolume: "tenant-skills", AllowedScripts: []string{"scripts/registered.py"}}
	if _, err := BuildContainerSpec(request, version); err == nil {
		t.Fatal("unregistered script must be rejected regardless of request content")
	}
}
