package file

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/aliyun/alibabacloud-oss-go-sdk-v2/oss"
	osscredentials "github.com/aliyun/alibabacloud-oss-go-sdk-v2/oss/credentials"
	aliyuncredentials "github.com/aliyun/credentials-go/credentials"
)

// Role identity belongs to the deployment, never to tenant-supplied storage
// settings. Keep the temporary keys in the SDK provider, not in persisted config.
type ossRoleConfig struct {
	accessKey, secretKey, roleARN, sessionName, policy, externalID string
	expiration                                                     int
}

var ossRoleCache struct {
	sync.Mutex
	config   ossRoleConfig
	provider osscredentials.CredentialsProvider
}

func ossCredentialsFromEnvironment() (osscredentials.CredentialsProvider, error) {
	authType := strings.ToLower(strings.TrimSpace(os.Getenv("OSS_AUTH_TYPE")))
	accessKey, secretKey := os.Getenv("OSS_ACCESS_KEY"), os.Getenv("OSS_SECRET_KEY")
	if accessKey == "" || secretKey == "" {
		return nil, errors.New("OSS_ACCESS_KEY and OSS_SECRET_KEY are required")
	}
	switch authType {
	case "", "access_key":
		return osscredentials.NewStaticCredentialsProvider(accessKey, secretKey, ""), nil
	case "ram_role_arn":
	default:
		return nil, errors.New("OSS_AUTH_TYPE must be access_key or ram_role_arn")
	}
	config := ossRoleConfig{
		accessKey: accessKey, secretKey: secretKey,
		roleARN:     strings.TrimSpace(os.Getenv("OSS_ROLE_ARN")),
		sessionName: strings.TrimSpace(os.Getenv("OSS_ROLE_SESSION_NAME")),
		policy:      strings.TrimSpace(os.Getenv("OSS_POLICY")),
		externalID:  strings.TrimSpace(os.Getenv("OSS_EXTERNAL_ID")),
		expiration:  3600,
	}
	if config.roleARN == "" {
		return nil, errors.New("OSS_ROLE_ARN is required for ram_role_arn")
	}
	if config.sessionName == "" {
		config.sessionName = "weknora"
	}
	if value := strings.TrimSpace(os.Getenv("OSS_ROLE_SESSION_EXPIRATION")); value != "" {
		duration, err := strconv.Atoi(value)
		if err != nil || duration < 900 || duration > 43200 {
			return nil, errors.New("OSS_ROLE_SESSION_EXPIRATION must be between 900 and 43200 seconds")
		}
		config.expiration = duration
	}
	if config.policy != "" && !json.Valid([]byte(config.policy)) {
		return nil, errors.New("OSS_POLICY must be valid JSON")
	}
	ossRoleCache.Lock()
	defer ossRoleCache.Unlock()
	if ossRoleCache.provider != nil && ossRoleCache.config == config {
		return ossRoleCache.provider, nil
	}
	provider, err := newOSSRoleCredentials(&aliyuncredentials.Config{
		Type:        oss.Ptr("ram_role_arn"),
		AccessKeyId: oss.Ptr(config.accessKey), AccessKeySecret: oss.Ptr(config.secretKey),
		RoleArn: oss.Ptr(config.roleARN), RoleSessionName: oss.Ptr(config.sessionName), Policy: oss.Ptr(config.policy),
		ExternalId: oss.Ptr(config.externalID), RoleSessionExpiration: oss.Ptr(config.expiration),
		// Pin the STS owner instead of inheriting an unrelated SDK default chain.
		STSEndpoint: oss.Ptr("sts.aliyuncs.com"), Timeout: oss.Ptr(5000), ConnectTimeout: oss.Ptr(5000),
	})
	if err != nil {
		return nil, err
	}
	ossRoleCache.config, ossRoleCache.provider = config, provider
	return provider, nil
}

type ossRoleCredentials struct {
	refresh chan struct{}
	source  aliyuncredentials.Credential
}

func newOSSRoleCredentials(config *aliyuncredentials.Config) (osscredentials.CredentialsProvider, error) {
	source, err := aliyuncredentials.NewCredential(config)
	if err != nil {
		return nil, errors.New("invalid OSS AssumeRole configuration")
	}
	return &ossRoleCredentials{source: source, refresh: make(chan struct{}, 1)}, nil
}

func (p *ossRoleCredentials) GetCredentials(ctx context.Context) (osscredentials.Credentials, error) {
	// credentials-go owns caching and refresh, but its role provider is mutable.
	// Serialize access across SDK requests and the main/temporary bucket clients.
	select {
	case p.refresh <- struct{}{}:
	case <-ctx.Done():
		return osscredentials.Credentials{}, ctx.Err()
	}
	defer func() { <-p.refresh }()
	if err := ctx.Err(); err != nil {
		return osscredentials.Credentials{}, err
	}
	value, err := p.source.GetCredential()
	if contextErr := ctx.Err(); contextErr != nil {
		return osscredentials.Credentials{}, contextErr
	}
	if err != nil {
		// SDK errors may contain the STS response body. Never expose credentials in
		// errors, and never fall back to the source AK when AssumeRole fails.
		return osscredentials.Credentials{}, errors.New(
			"OSS AssumeRole failed; check role trust, STS permissions, policy and network access")
	}
	if value == nil || value.AccessKeyId == nil || *value.AccessKeyId == "" ||
		value.AccessKeySecret == nil || *value.AccessKeySecret == "" ||
		value.SecurityToken == nil || *value.SecurityToken == "" {
		return osscredentials.Credentials{}, errors.New("OSS AssumeRole returned incomplete credentials")
	}
	return osscredentials.Credentials{
		AccessKeyID: *value.AccessKeyId, AccessKeySecret: *value.AccessKeySecret,
		SecurityToken: *value.SecurityToken,
	}, nil
}

// NewEnvironmentOssFileService is only for trusted environment-owned backends.
// Tenant storage continues to use NewOssFileServiceWithTempBucket with its own keys.
func NewEnvironmentOssFileService(config *types.OSSEngineConfig) (interfaces.FileService, error) {
	if config == nil || config.Endpoint == "" || config.Region == "" || config.BucketName == "" {
		return nil, errors.New("missing OSS configuration")
	}
	prefix := config.PathPrefix
	if prefix == "" {
		prefix = "weknora/"
	}
	tempBucket := ""
	if config.UseTempBucket {
		tempBucket = config.TempBucketName
	}
	if auth := strings.ToLower(strings.TrimSpace(os.Getenv("OSS_AUTH_TYPE"))); auth == "" || auth == "access_key" {
		return NewOssFileServiceWithTempBucket(
			config.Endpoint, config.Region, config.AccessKey, config.SecretKey,
			config.BucketName, prefix, tempBucket, config.TempRegion)
	}
	creds, err := ossCredentialsFromEnvironment()
	if err != nil {
		return nil, err
	}
	return newOssFileServiceWithCredentials(
		config.Endpoint, config.Region, config.BucketName, prefix, tempBucket, config.TempRegion, creds)
}

// CheckEnvironmentOssConnectivity uses the same role identity as transfers.
func CheckEnvironmentOssConnectivity(ctx context.Context, config *types.OSSEngineConfig) error {
	if config == nil {
		return errors.New("missing OSS configuration")
	}
	if auth := strings.ToLower(strings.TrimSpace(os.Getenv("OSS_AUTH_TYPE"))); auth == "" || auth == "access_key" {
		return CheckOssConnectivity(
			ctx, config.Endpoint, config.Region, config.AccessKey, config.SecretKey, config.BucketName)
	}
	creds, err := ossCredentialsFromEnvironment()
	if err != nil {
		return err
	}
	return checkOSSConnectivityWithCredentials(ctx, config.Endpoint, config.Region, config.BucketName, creds)
}
