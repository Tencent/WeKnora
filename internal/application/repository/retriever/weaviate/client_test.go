package weaviate

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	pb "github.com/weaviate/weaviate/grpc/generated/protocol/v1"
)

// Weaviate counts integrations by the X-Weaviate-Client-Integration header, so
// it has to ride on REST and gRPC requests alike: searches, inserts and
// deletes all go over gRPC.
func TestClientSendsTheIntegrationHeaderOverRESTAndGRPC(t *testing.T) {
	previous := IntegrationVersion
	IntegrationVersion = "0.8.3"
	t.Cleanup(func() { IntegrationVersion = previous })

	fake := &fakeWeaviate{
		search: func(*pb.SearchRequest) (*pb.SearchReply, error) { return &pb.SearchReply{}, nil },
		rest: func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"class":"Weknora_embeddings_2"}`)) // the class exists
		},
	}
	repo := fake.repository(t, "Weknora_embeddings")

	_, err := repo.VectorRetrieve(context.Background(), types.RetrieveParams{Embedding: []float32{1, 0}, TopK: 1})
	require.NoError(t, err)

	fake.mu.Lock()
	defer fake.mu.Unlock()
	require.NotEmpty(t, fake.restHeaders)
	for _, header := range fake.restHeaders {
		assert.Equal(t, "weknora/0.8.3", header.Get("X-Weaviate-Client-Integration"))
	}
	require.NotEmpty(t, fake.grpcHeaders, "the search should have gone over gRPC")
	for _, md := range fake.grpcHeaders {
		assert.Equal(t, []string{"weknora/0.8.3"}, md.Get("x-weaviate-client-integration"))
	}
}

func TestIntegrationHeaderValueFallsBackToUnknown(t *testing.T) {
	previous := IntegrationVersion
	t.Cleanup(func() { IntegrationVersion = previous })

	IntegrationVersion = " "
	assert.Equal(t, "weknora/unknown", integrationHeaderValue())
	IntegrationVersion = "v0.8.3"
	assert.Equal(t, "weknora/v0.8.3", integrationHeaderValue())
}

// WeKnora stores Weaviate addresses as "host:port" pairs, the shape the v5
// client took; v6 wants host and port apart.
func TestClientOptionsSplitAddresses(t *testing.T) {
	cases := []struct {
		name                       string
		cfg                        ClientConfig
		scheme, restHost, restPort string
		grpcHost, grpcPort         string
		wantErr                    bool
	}{
		{
			name:   "docker compose defaults",
			cfg:    ClientConfig{Host: "weaviate:8080", GrpcAddress: "weaviate:50051"},
			scheme: "http", restHost: "weaviate", restPort: "8080", grpcHost: "weaviate", grpcPort: "50051",
		},
		{
			name: "https without ports uses 443 and the gRPC default",
			cfg: ClientConfig{
				Host: "example.weaviate.cloud", Scheme: "https", GrpcAddress: "grpc-example.weaviate.cloud:443",
			},
			scheme: "https", restHost: "example.weaviate.cloud", restPort: "443",
			grpcHost: "grpc-example.weaviate.cloud", grpcPort: "443",
		},
		{
			name:   "scheme carried in the host",
			cfg:    ClientConfig{Host: "https://example.com/", GrpcAddress: "example.com:50051"},
			scheme: "https", restHost: "example.com", restPort: "443", grpcHost: "example.com", grpcPort: "50051",
		},
		{
			name:   "missing gRPC address follows the REST host",
			cfg:    ClientConfig{Host: "10.0.0.5:8080"},
			scheme: "http", restHost: "10.0.0.5", restPort: "8080", grpcHost: "10.0.0.5", grpcPort: "50051",
		},
		{
			name:   "IPv6 literals",
			cfg:    ClientConfig{Host: "[::1]:8080", GrpcAddress: "[::1]"},
			scheme: "http", restHost: "::1", restPort: "8080", grpcHost: "::1", grpcPort: "50051",
		},
		{name: "empty host", cfg: ClientConfig{}, wantErr: true},
		{name: "unsupported scheme", cfg: ClientConfig{Host: "weaviate:8080", Scheme: "ftp"}, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			scheme, restHost, restPort, grpcHost, grpcPort, err := resolveAddresses(tc.cfg)
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.scheme, scheme)
			assert.Equal(t, tc.restHost, restHost)
			assert.Equal(t, tc.restPort, restPort)
			assert.Equal(t, tc.grpcHost, grpcHost)
			assert.Equal(t, tc.grpcPort, grpcPort)
		})
	}
}

// An older server accepts the connection and then answers searches wrongly, so
// it has to be refused while connecting, with a message that names the fix.
func TestNewClientRefusesAServerOlderThanTheMinimum(t *testing.T) {
	for version, supported := range map[string]bool{
		"1.28.4": false, "1.32.27": false, "0.9.0": false,
		"1.33.0": true, "1.33.18": true, "1.40.5": true, "1.41.0-rc.0": true, "v1.39.16": true, "2.0.0": true,
		"": true, "dev": true, "1.x": true,
	} {
		t.Run(version, func(t *testing.T) {
			err := checkServerVersion(version)
			if supported {
				require.NoError(t, err)
				return
			}
			require.ErrorIs(t, err, ErrUnsupportedServer)
			assert.ErrorContains(t, err, version)
			assert.ErrorContains(t, err, "1.33")
		})
	}

	fake := &fakeWeaviate{version: "1.28.4"}
	fake.start(t)
	_, err := NewClient(context.Background(), ClientConfig{Host: fake.restAddress, GrpcAddress: fake.grpcAddress})
	require.ErrorIs(t, err, ErrUnsupportedServer)
}

// WeKnora and Weaviate usually start together, and Weaviate takes longer. The
// engine must not be lost for the life of the process because of that.
func TestNewClientWithRetryWaitsForAServerThatIsStillStarting(t *testing.T) {
	fake := &fakeWeaviate{metaFailures: 1}
	fake.start(t)
	cfg := ClientConfig{Host: fake.restAddress, GrpcAddress: fake.grpcAddress}

	_, err := NewClient(context.Background(), cfg)
	require.Error(t, err, "a single attempt fails while the server is starting")

	fake.mu.Lock()
	fake.metaFailures = 1
	fake.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	client, err := NewClientWithRetry(ctx, cfg)
	require.NoError(t, err)
	require.NoError(t, client.Close())
}

func TestNewClientWithRetryGivesUp(t *testing.T) {
	t.Run("when the context ends", func(t *testing.T) {
		fake := &fakeWeaviate{metaFailures: 1 << 30}
		fake.start(t)
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()
		_, err := NewClientWithRetry(ctx, ClientConfig{Host: fake.restAddress, GrpcAddress: fake.grpcAddress})
		require.Error(t, err)
	})
	t.Run("at once on a server that is too old", func(t *testing.T) {
		fake := &fakeWeaviate{version: "1.28.4"}
		fake.start(t)
		started := time.Now()
		_, err := NewClientWithRetry(context.Background(),
			ClientConfig{Host: fake.restAddress, GrpcAddress: fake.grpcAddress})
		require.ErrorIs(t, err, ErrUnsupportedServer)
		assert.Less(t, time.Since(started), connectRetryInterval, "waiting does not make the server newer")
	})
	t.Run("at once on a configuration it cannot use", func(t *testing.T) {
		_, err := NewClientWithRetry(context.Background(), ClientConfig{Host: "localhost:8080", Scheme: "ftp"})
		require.ErrorContains(t, err, "unsupported scheme")
	})
}
