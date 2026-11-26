package lite_smoke_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBootstrapAdminWithRegistrationDisabled(t *testing.T) {
	workDir, binPath := buildLiteServer(t)
	const (
		email        = "bootstrap@example.com"
		password     = "InitialPassword!2026"
		newPassword  = "DifferentPassword!2026"
		weakPassword = "weakpw"
		otherEmail   = "other@example.com"
	)
	start := func(email, password string) (*http.Client, string, func()) {
		t.Helper()
		return startLiteServer(t, workDir, binPath,
			"DISABLE_REGISTRATION=true",
			"WEKNORA_BOOTSTRAP_SYSTEM_ADMIN_EMAIL="+email,
			"WEKNORA_BOOTSTRAP_SYSTEM_ADMIN_PASSWORD="+password,
		)
	}
	checkLogin := func(client *http.Client, baseURL, email, password string, status int) string {
		t.Helper()
		var response struct {
			Success bool   `json:"success"`
			Token   string `json:"token"`
			User    struct {
				ID            string `json:"id"`
				IsSystemAdmin bool   `json:"is_system_admin"`
			} `json:"user"`
		}
		doJSON(t, client, http.MethodPost, baseURL+"/api/v1/auth/login", "", map[string]string{
			"email": email, "password": password,
		}, status, &response)
		if status == http.StatusOK {
			require.True(t, response.Success)
			require.True(t, response.User.IsSystemAdmin)
			require.NotEmpty(t, response.User.ID)
			require.NotEmpty(t, response.Token)
		}
		return response.User.ID
	}

	// The email alone does not create a new account.
	client, baseURL, stop := start(email, "")
	checkLogin(client, baseURL, email, password, http.StatusUnauthorized)
	stop()

	// A password that fails the normal policy cannot provision an account.
	client, baseURL, stop = start(email, weakPassword)
	checkLogin(client, baseURL, email, weakPassword, http.StatusUnauthorized)
	stop()

	client, baseURL, stop = start(email, password)
	for _, path := range []string{"/auth/register", "/auth/register-by-invite"} {
		doJSON(t, client, http.MethodPost, baseURL+"/api/v1"+path, "", map[string]string{
			"username": "another-user", "email": otherEmail, "password": password,
		}, http.StatusForbidden, nil)
	}
	id := checkLogin(client, baseURL, email, password, http.StatusOK)
	stop()

	// Restarts do not replace the credential or create a second admin.
	client, baseURL, stop = start(email, newPassword)
	checkLogin(client, baseURL, email, newPassword, http.StatusUnauthorized)
	require.Equal(t, id, checkLogin(client, baseURL, email, password, http.StatusOK))
	stop()

	client, baseURL, _ = start(otherEmail, newPassword)
	checkLogin(client, baseURL, otherEmail, newPassword, http.StatusUnauthorized)
	require.Equal(t, id, checkLogin(client, baseURL, email, password, http.StatusOK))
}
