package skillrunner

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
)

func NewHandler(executor *Executor, credential string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"status":"ok"}`))
	})
	mux.Handle("POST /v1/execute", RequireCredential(credential, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		request.Body = http.MaxBytesReader(writer, request.Body, MaxRequestBytes)
		decoder := json.NewDecoder(request.Body)
		decoder.DisallowUnknownFields()
		var input ExecuteRequest
		if err := decoder.Decode(&input); err != nil {
			http.Error(writer, ErrInvalidRequest.Error(), http.StatusBadRequest)
			return
		}
		if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
			http.Error(writer, ErrInvalidRequest.Error(), http.StatusBadRequest)
			return
		}
		result, err := executor.Execute(request.Context(), input)
		if err != nil {
			http.Error(writer, err.Error(), http.StatusBadRequest)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(result)
	})))
	return mux
}
