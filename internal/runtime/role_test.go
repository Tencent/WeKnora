package runtime

import "testing"

func TestResolveAppRole(t *testing.T) {
	cases := []struct {
		name string
		env  string
		want AppRole
	}{
		{"empty defaults to all", "", RoleAll},
		{"explicit all", "all", RoleAll},
		{"api", "api", RoleAPI},
		{"worker", "worker", RoleWorker},
		{"uppercase normalized", "WORKER", RoleWorker},
		{"whitespace trimmed", "  api  ", RoleAPI},
		{"unknown falls back to all", "frontend", RoleAll},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("APP_ROLE", c.env)
			if got := ResolveAppRole(); got != c.want {
				t.Fatalf("ResolveAppRole() with APP_ROLE=%q = %q, want %q", c.env, got, c.want)
			}
		})
	}
}

func TestAppRolePredicates(t *testing.T) {
	cases := []struct {
		role          AppRole
		runsWorker    bool
		runsScheduler bool
	}{
		{RoleAll, true, true},
		{RoleAPI, false, true},
		{RoleWorker, true, false},
	}
	for _, c := range cases {
		t.Run(string(c.role), func(t *testing.T) {
			if got := c.role.RunsWorker(); got != c.runsWorker {
				t.Errorf("%q.RunsWorker() = %v, want %v", c.role, got, c.runsWorker)
			}
			if got := c.role.RunsScheduler(); got != c.runsScheduler {
				t.Errorf("%q.RunsScheduler() = %v, want %v", c.role, got, c.runsScheduler)
			}
		})
	}
}
