package skillrunner

import (
	"crypto/subtle"
	"net/http"
	"strings"
)

func RequireCredential(credential string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		provided := strings.TrimPrefix(request.Header.Get("Authorization"), "Bearer ")
		if credential == "" || len(provided) != len(credential) || subtle.ConstantTimeCompare([]byte(provided), []byte(credential)) != 1 {
			http.Error(writer, ErrUnauthorized.Error(), http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(writer, request)
	})
}
