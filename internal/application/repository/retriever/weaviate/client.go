package weaviate

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/weaviate/weaviate-go-client/v6"
	"github.com/weaviate/weaviate-go-client/v6/query"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	// headerIntegration tells Weaviate which integration a request comes from,
	// alongside the X-Weaviate-Client header the SDK sets itself. The client
	// sends it on REST and gRPC requests alike.
	headerIntegration = "X-Weaviate-Client-Integration"
	integrationName   = "weknora"

	defaultGRPCPort = "50051"

	// The oldest Weaviate this repository works against. Older servers accept
	// the connection and then answer searches wrongly: 1.28, for one, does not
	// know the ContainsNone filter operator and ignores the named vector a
	// search targets.
	minServerMajor, minServerMinor = 1, 33

	// grpcProbeCollection is the collection the gRPC probe searches. It does
	// not exist: the probe only needs the server to answer.
	grpcProbeCollection = "WeknoraConnectionProbe"
	// grpcProbeTimeout bounds the probe, so an address that swallows packets
	// does not use up the caller's whole connect budget.
	grpcProbeTimeout = 5 * time.Second

	// connectRetryInterval is how long NewClientWithRetry waits between
	// attempts.
	connectRetryInterval = 2 * time.Second
)

// ErrUnsupportedServer is returned when the Weaviate server is older than the
// oldest version this repository supports.
var ErrUnsupportedServer = errors.New("weaviate: unsupported server version")

// ErrGRPCUnavailable is returned when the REST address answers but the gRPC
// address does not, or refuses the credentials. Search, batch insert and batch
// delete all go over gRPC, so such a client could not do its job.
var ErrGRPCUnavailable = errors.New("weaviate: gRPC endpoint unavailable")

// IntegrationVersion is the WeKnora version reported in the
// X-Weaviate-Client-Integration header. The container sets it from the version
// injected at build time; this package cannot import that one directly.
var IntegrationVersion = "unknown"

// ClientConfig is how WeKnora addresses a Weaviate instance, both from the
// environment and from a vector store saved in the UI.
type ClientConfig struct {
	// Host is the REST address, "host:port". A host without a port uses the
	// scheme's default port. A leading "http://" or "https://" is accepted and
	// stands in for Scheme when Scheme is empty.
	Host string
	// GrpcAddress is the gRPC address, "host:port". It defaults to Host's host
	// on port 50051.
	GrpcAddress string
	// Scheme is "http" or "https". With "https" gRPC uses TLS as well.
	Scheme string
	// APIKey enables API key authentication when non-empty.
	APIKey string
}

// NewClient connects to Weaviate. The SDK reads /v1/meta before returning, so
// an unreachable REST address fails here rather than on the first query. So
// does a server too old to be used (ErrUnsupportedServer) and a gRPC address
// that does not answer (ErrGRPCUnavailable).
func NewClient(ctx context.Context, cfg ClientConfig) (*weaviate.Client, error) {
	options, err := clientOptions(cfg)
	if err != nil {
		return nil, err
	}
	return connect(ctx, options)
}

// NewClientWithRetry is NewClient for a server that may still be starting, as
// it is when WeKnora and Weaviate come up together: it keeps trying until the
// server answers or ctx ends. A configuration the client cannot use and a
// server that is too old are returned at once; waiting does not fix either.
func NewClientWithRetry(ctx context.Context, cfg ClientConfig) (*weaviate.Client, error) {
	options, err := clientOptions(cfg)
	if err != nil {
		return nil, err
	}
	for {
		client, err := connect(ctx, options)
		if err == nil || errors.Is(err, ErrUnsupportedServer) {
			return client, err
		}
		select {
		case <-ctx.Done():
			return nil, err
		case <-time.After(connectRetryInterval):
		}
	}
}

func connect(ctx context.Context, options []weaviate.Option) (*weaviate.Client, error) {
	client, err := weaviate.NewClient(ctx, options...)
	if err != nil {
		return nil, err
	}
	meta, err := client.Metadata(ctx)
	if err == nil {
		err = checkServerVersion(meta.Version)
	}
	if err == nil {
		err = probeGRPC(ctx, client)
	}
	if err != nil {
		_ = client.Close()
		return nil, err
	}
	return client, nil
}

// probeGRPC checks that the gRPC address answers. The SDK opens its gRPC
// channel lazily, so without this a wrong address goes unnoticed until the
// first search. The probe searches a collection that does not exist: a server
// that says so has been reached and has accepted the credentials.
func probeGRPC(ctx context.Context, client *weaviate.Client) error {
	ctx, cancel := context.WithTimeout(ctx, grpcProbeTimeout)
	defer cancel()
	_, err := client.Collections.Use(grpcProbeCollection).Query.OverAll(ctx, query.OverAll{Limit: 1})
	if err == nil {
		return nil
	}
	switch status.Code(err) {
	case codes.Unavailable, codes.DeadlineExceeded, codes.Unauthenticated:
		return fmt.Errorf("%w: %w", ErrGRPCUnavailable, err)
	}
	if ctx.Err() != nil {
		return fmt.Errorf("%w: %w", ErrGRPCUnavailable, err)
	}
	return nil
}

// checkServerVersion refuses a server older than the minimum. A version it
// cannot read is let through: a custom build is not evidence of an old server.
func checkServerVersion(version string) error {
	parts := strings.SplitN(strings.TrimPrefix(strings.TrimSpace(version), "v"), ".", 3)
	if len(parts) < 2 {
		return nil
	}
	major, errMajor := strconv.Atoi(parts[0])
	minor, errMinor := strconv.Atoi(parts[1])
	if errMajor != nil || errMinor != nil {
		return nil
	}
	if major < minServerMajor || (major == minServerMajor && minor < minServerMinor) {
		return fmt.Errorf("%w: found %s, need %d.%d or newer",
			ErrUnsupportedServer, version, minServerMajor, minServerMinor)
	}
	return nil
}

func clientOptions(cfg ClientConfig) ([]weaviate.Option, error) {
	scheme, restHost, restPort, grpcHost, grpcPort, err := resolveAddresses(cfg)
	if err != nil {
		return nil, err
	}
	options := []weaviate.Option{
		weaviate.WithScheme(scheme),
		weaviate.WithHTTPHost(sdkHost(restHost)),
		weaviate.WithHTTPPort(restPort),
		weaviate.WithGRPCHost(sdkHost(grpcHost)),
		weaviate.WithGRPCPort(grpcPort),
		weaviate.WithHeader(http.Header{headerIntegration: {integrationHeaderValue()}}),
	}
	if apiKey := strings.TrimSpace(cfg.APIKey); apiKey != "" {
		options = append(options, weaviate.WithAPIKey(apiKey))
	}
	return options, nil
}

// resolveAddresses turns cfg into the scheme, hosts and ports the client
// dials.
func resolveAddresses(cfg ClientConfig) (scheme, restHost, restPort, grpcHost, grpcPort string, err error) {
	scheme = strings.ToLower(strings.TrimSpace(cfg.Scheme))
	restAddress := strings.TrimSpace(cfg.Host)
	for _, prefix := range []string{"http://", "https://"} {
		if len(restAddress) >= len(prefix) && strings.EqualFold(restAddress[:len(prefix)], prefix) {
			if scheme == "" {
				scheme = strings.TrimSuffix(prefix, "://")
			}
			restAddress = strings.TrimRight(restAddress[len(prefix):], "/")
		}
	}
	if scheme == "" {
		scheme = "http"
	}
	if scheme != "http" && scheme != "https" {
		return "", "", "", "", "", fmt.Errorf("weaviate: unsupported scheme %q", cfg.Scheme)
	}

	defaultRESTPort := "80"
	if scheme == "https" {
		defaultRESTPort = "443"
	}
	restHost, restPort, err = splitAddress(restAddress, defaultRESTPort)
	if err != nil {
		return "", "", "", "", "", fmt.Errorf("weaviate: invalid host %q: %w", cfg.Host, err)
	}

	grpcAddress := strings.TrimSpace(cfg.GrpcAddress)
	if grpcAddress == "" {
		grpcAddress = net.JoinHostPort(restHost, defaultGRPCPort)
	}
	grpcHost, grpcPort, err = splitAddress(grpcAddress, defaultGRPCPort)
	if err != nil {
		return "", "", "", "", "", fmt.Errorf("weaviate: invalid gRPC address %q: %w", cfg.GrpcAddress, err)
	}
	return scheme, restHost, restPort, grpcHost, grpcPort, nil
}

// sdkHost is host in the form the SDK needs. It builds the REST URL and the
// gRPC target by joining host and port with a colon, which is only an address
// for an IPv6 literal when the literal is in brackets.
func sdkHost(host string) string {
	if strings.Contains(host, ":") {
		return "[" + host + "]"
	}
	return host
}

func integrationHeaderValue() string {
	version := strings.TrimSpace(IntegrationVersion)
	if version == "" {
		version = "unknown"
	}
	return integrationName + "/" + version
}

// splitAddress splits "host:port", falling back to defaultPort when address
// carries no port.
func splitAddress(address, defaultPort string) (string, string, error) {
	if address == "" {
		return "", "", fmt.Errorf("address is empty")
	}
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		// No port: the whole address is the host. Bracketed IPv6 literals keep
		// their brackets out of the host name.
		host, port = strings.TrimSuffix(strings.TrimPrefix(address, "["), "]"), defaultPort
	}
	if host == "" {
		return "", "", fmt.Errorf("host is empty")
	}
	if port == "" {
		port = defaultPort
	}
	return host, port, nil
}
