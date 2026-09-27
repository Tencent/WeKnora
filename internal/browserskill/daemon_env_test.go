package browserskill

import (
	"slices"
	"testing"
)

// The browser daemon, readable by other processes of WeKnora's user, gets
// none of WeKnora's secrets but keeps what a browser needs.
func TestDaemonEnvDropsSecrets(t *testing.T) {
	got := daemonEnv([]string{
		"PATH=/usr/bin", "HOME=/home/app", "LANG=C.UTF-8", "TZ=UTC", "HTTPS_PROXY=http://proxy:3128",
		"DISPLAY=:0", "BROWSERSKILL_MAX_CONNECTIONS=4",
		"SYSTEM_AES_KEY=k", "TENANT_AES_KEY=k", "JWT_SECRET=s", "DB_PASSWORD=p", "DB_HOST=db",
		"REDIS_PASSWORD=r", "MINIO_ACCESS_KEY_ID=a", "OPENAI_API_KEY=o", "BROWSERSKILL_CLUSTER_SECRET=c",
		"DATABASE_URL=postgres://u:p@db/x", "AWS_SECRET_ACCESS_KEY=x", "NEO4J_URI=bolt://x",
	})
	want := []string{
		"PATH=/usr/bin", "HOME=/home/app", "LANG=C.UTF-8", "TZ=UTC", "HTTPS_PROXY=http://proxy:3128",
		"DISPLAY=:0", "BROWSERSKILL_MAX_CONNECTIONS=4",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("daemon env = %v", got)
	}
}
