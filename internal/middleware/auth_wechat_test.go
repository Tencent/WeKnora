package middleware

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestWeChatPublicRoutesAreMethodScoped(t *testing.T) {
	for path, method := range map[string]string{
		"attempts": http.MethodPost, "exchange": http.MethodPost, "complete": http.MethodPost,
		"start": http.MethodGet, "callback": http.MethodGet,
	} {
		route := "/api/v1/auth/wechat/" + path
		require.True(t, isNoAuthAPI(route, method))
		require.False(t, isNoAuthAPI(route, http.MethodDelete))
		require.False(t, isNoAuthAPI(route+"/other", method))
	}
	require.False(t, isNoAuthAPI("/api/v1/wechat/qrcode", http.MethodPost))
}
