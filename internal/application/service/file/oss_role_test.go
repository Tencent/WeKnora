package file

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/utils"
	"github.com/aliyun/alibabacloud-oss-go-sdk-v2/oss"
	osscredentials "github.com/aliyun/alibabacloud-oss-go-sdk-v2/oss/credentials"
	aliyuncredentials "github.com/aliyun/credentials-go/credentials"
	"github.com/stretchr/testify/require"
)

func roleTestEnvironment(t *testing.T) ossRoleConfig {
	t.Helper()
	for key, value := range map[string]string{
		"OSS_AUTH_TYPE": "ram_role_arn", "OSS_ACCESS_KEY": "source-key", "OSS_SECRET_KEY": "source-secret",
		"OSS_ROLE_ARN": "acs:ram::1234567890123456:role/weknora", "OSS_ROLE_SESSION_NAME": "weknora-test",
		"OSS_POLICY":      osRolePolicy,
		"OSS_EXTERNAL_ID": "external-test", "OSS_ROLE_SESSION_EXPIRATION": "1800",
	} {
		t.Setenv(key, value)
	}
	return ossRoleConfig{
		accessKey: "source-key", secretKey: "source-secret",
		roleARN: "acs:ram::1234567890123456:role/weknora", sessionName: "weknora-test",
		policy: osRolePolicy, externalID: "external-test", expiration: 1800,
	}
}

const osRolePolicy = `{"Version":"1","Statement":[{"Effect":"Allow","Action":["oss:GetObject"],"Resource":["*"]}]}`

func roleTestProvider(t *testing.T, handler http.HandlerFunc) osscredentials.CredentialsProvider {
	t.Helper()
	server := httptest.NewTLSServer(handler)
	t.Cleanup(server.Close)
	// credentials-go clones the default transport. Trust only this local fixture
	// for the duration of the test; no cloud request or credentials are involved.
	previous := http.DefaultTransport
	http.DefaultTransport = server.Client().Transport
	t.Cleanup(func() { http.DefaultTransport = previous })
	provider, err := newOSSRoleCredentials(&aliyuncredentials.Config{
		Type: oss.Ptr("ram_role_arn"), AccessKeyId: oss.Ptr("source-key"), AccessKeySecret: oss.Ptr("source-secret"),
		RoleArn: oss.Ptr("acs:ram::1234567890123456:role/weknora"), RoleSessionName: oss.Ptr("weknora-test"),
		Policy: oss.Ptr(osRolePolicy), ExternalId: oss.Ptr("external-test"), RoleSessionExpiration: oss.Ptr(1800),
		STSEndpoint: oss.Ptr(strings.TrimPrefix(server.URL, "https://")),
		Timeout:     oss.Ptr(1000), ConnectTimeout: oss.Ptr(1000),
	})
	require.NoError(t, err)
	return provider
}

func writeRoleCredentials(w http.ResponseWriter, key string, expiry time.Time) {
	_ = json.NewEncoder(w).Encode(map[string]any{"Credentials": map[string]string{
		"AccessKeyId": key, "AccessKeySecret": "temporary-secret",
		"SecurityToken": "temporary-token-" + key, "Expiration": expiry.UTC().Format(time.RFC3339),
	}})
}

func TestOSSRoleSDKRefreshAndSigning(t *testing.T) {
	var calls atomic.Int32
	provider := roleTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		for key, want := range map[string]string{
			"Action": "AssumeRole", "RoleArn": "acs:ram::1234567890123456:role/weknora",
			"RoleSessionName": "weknora-test", "DurationSeconds": "1800", "Policy": osRolePolicy,
			"ExternalId": "external-test", "AccessKeyId": "source-key",
		} {
			if got := r.Form.Get(key); got != want {
				t.Errorf("%s=%q, want %q", key, got, want)
			}
		}
		if r.Form.Get("Signature") == "" {
			t.Error("missing STS signature")
		}
		call := calls.Add(1)
		expires := time.Now().Add(time.Hour)
		if call == 1 {
			expires = time.Now().Add(time.Minute)
		}
		writeRoleCredentials(w, fmt.Sprintf("temporary-%d", call), expires)
	})
	first, err := provider.GetCredentials(context.Background())
	require.NoError(t, err)
	require.Equal(t, "temporary-1", first.AccessKeyID)
	second, err := provider.GetCredentials(context.Background())
	require.NoError(t, err)
	require.Equal(t, "temporary-2", second.AccessKeyID)
	client, err := newOSSClientWithCredentials("https://oss-cn-hangzhou.aliyuncs.com", "cn-hangzhou", provider)
	require.NoError(t, err)
	for _, request := range []any{
		&oss.GetObjectRequest{Bucket: oss.Ptr("test-bucket"), Key: oss.Ptr("file.txt")},
		&oss.PutObjectRequest{Bucket: oss.Ptr("test-bucket"), Key: oss.Ptr("file.txt")},
	} {
		signed, err := client.Presign(context.Background(), request)
		require.NoError(t, err)
		parsed, err := url.Parse(signed.URL)
		require.NoError(t, err)
		require.Contains(t, parsed.Query().Get("x-oss-credential"), "temporary-2/")
		require.Equal(t, "temporary-token-temporary-2", parsed.Query().Get("x-oss-security-token"))
		require.NotEmpty(t, parsed.Query().Get("x-oss-signature"))
	}
	require.EqualValues(t, 2, calls.Load(), "fresh role credentials should be reused")
}

func TestOSSRoleConcurrentRequestsShareRefresh(t *testing.T) {
	var calls atomic.Int32
	provider := roleTestProvider(t, func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		writeRoleCredentials(w, "temporary", time.Now().Add(time.Hour))
	})
	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() {
			value, err := provider.GetCredentials(context.Background())
			if err != nil {
				t.Error(err)
				return
			}
			if value.AccessKeyID != "temporary" {
				t.Errorf("unexpected key %q", value.AccessKeyID)
			}
		})
	}
	wg.Wait()
	require.EqualValues(t, 1, calls.Load())
}

func TestOSSRoleFailureDoesNotExposeResponseOrUseSourceKey(t *testing.T) {
	provider := roleTestProvider(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, `{"secret":"response-secret","Code":"Forbidden"}`)
	})
	value, err := provider.GetCredentials(context.Background())
	require.ErrorContains(t, err, "OSS AssumeRole failed")
	require.NotContains(t, err.Error(), "response-secret")
	require.Empty(t, value.AccessKeyID)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = provider.GetCredentials(ctx)
	require.ErrorIs(t, err, context.Canceled)
}

func TestOSSEnvironmentRoleValidation(t *testing.T) {
	for _, tc := range []struct{ name, key, value, want string }{
		{"unknown mode", "OSS_AUTH_TYPE", "other", "OSS_AUTH_TYPE"},
		{"missing role", "OSS_ROLE_ARN", "", "OSS_ROLE_ARN"},
		{"short duration", "OSS_ROLE_SESSION_EXPIRATION", "899", "OSS_ROLE_SESSION_EXPIRATION"},
		{"long duration", "OSS_ROLE_SESSION_EXPIRATION", "43201", "OSS_ROLE_SESSION_EXPIRATION"},
		{"invalid duration", "OSS_ROLE_SESSION_EXPIRATION", "abc", "OSS_ROLE_SESSION_EXPIRATION"},
		{"invalid policy", "OSS_POLICY", "{", "OSS_POLICY"},
		{"missing source key", "OSS_ACCESS_KEY", "", "OSS_ACCESS_KEY"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			roleTestEnvironment(t)
			t.Setenv(tc.key, tc.value)
			_, err := ossCredentialsFromEnvironment()
			require.ErrorContains(t, err, tc.want)
		})
	}
	roleTestEnvironment(t)
	first, err := ossCredentialsFromEnvironment()
	require.NoError(t, err)
	second, err := ossCredentialsFromEnvironment()
	require.NoError(t, err)
	require.Same(t, first, second)
	t.Setenv("OSS_ROLE_ARN", "acs:ram::1234567890123456:role/rotated")
	third, err := ossCredentialsFromEnvironment()
	require.NoError(t, err)
	require.NotSame(t, first, third)
}

func TestOSSRoleEnvironmentBoundary(t *testing.T) {
	roleConfig := roleTestEnvironment(t)
	t.Setenv("OSS_AUTH_TYPE", "invalid")
	t.Setenv("SSRF_WHITELIST", "127.0.0.1")
	utils.ResetSSRFWhitelistForTest()
	t.Cleanup(utils.ResetSSRFWhitelistForTest)
	var keys []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		keys = append(keys, r.Header.Get("Authorization"))
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	config := types.StorageBackendConfig{
		Endpoint: server.URL, Region: "cn-hangzhou", BucketName: "bucket",
		AccessKeyID: "tenant-key", SecretAccessKey: "tenant-secret",
	}
	tenant := &types.StorageBackend{Provider: "oss", Source: types.StorageBackendSourceUser, Config: config}
	_, _, err := NewFileServiceFromStorageBackend(tenant, "")
	require.NoError(t, err)
	require.Contains(t, keys[0], "tenant-key/")
	system := *tenant
	system.Source = types.StorageBackendSourceEnv
	_, _, err = NewFileServiceFromStorageBackend(&system, "")
	require.ErrorContains(t, err, "OSS_AUTH_TYPE")
	require.Len(t, keys, 1, "invalid system auth must not fall back to the tenant/static key")
	var calls atomic.Int32
	roleProvider := roleTestProvider(t, func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		writeRoleCredentials(w, "assumed-key", time.Now().Add(time.Hour))
	})
	ossRoleCache.Lock()
	previousConfig, previousProvider := ossRoleCache.config, ossRoleCache.provider
	ossRoleCache.config, ossRoleCache.provider = roleConfig, roleProvider
	ossRoleCache.Unlock()
	t.Cleanup(func() {
		ossRoleCache.Lock()
		defer ossRoleCache.Unlock()
		ossRoleCache.config, ossRoleCache.provider = previousConfig, previousProvider
	})
	t.Setenv("OSS_AUTH_TYPE", "ram_role_arn")
	system.Config.UseTempBucket = true
	system.Config.TempBucketName = "temporary-bucket"
	driver, _, err := NewFileServiceFromStorageBackend(&system, "")
	require.NoError(t, err)
	for _, temporary := range []bool{false, true} {
		_, err = driver.SaveBytes(context.Background(), []byte("content"), 7, "file.txt", temporary)
		require.NoError(t, err)
	}
	for _, key := range keys[1:] {
		require.Contains(t, key, "assumed-key/")
		require.NotContains(t, key, "source-key/")
	}
	require.Len(t, keys, 5, "one tenant HEAD, two system HEADs and two PUTs")
	require.EqualValues(t, 1, calls.Load(), "both buckets share the role cache")
}
