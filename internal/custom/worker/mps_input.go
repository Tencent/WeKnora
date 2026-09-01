package worker

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	objstore "github.com/Tencent/WeKnora/internal/custom/client/minio"
	"github.com/Tencent/WeKnora/internal/custom/config"
	"github.com/Tencent/WeKnora/internal/custom/model"
	cos "github.com/tencentyun/cos-go-sdk-v5"
)

const mpsInputURLTTL = 6 * time.Hour

// TencentMPSInputPreparer uploads private/local videos to COS once, then gives
// MPS a short-lived signed URL. The object key is stable across retries.
type TencentMPSInputPreparer struct {
	MinIO     *objstore.Client
	Client    *cos.Client
	Bucket    string
	Region    string
	Prefix    string
	SecretID  string
	SecretKey string
}

func NewTencentMPSInputPreparer(cfg config.MPSConfig, minioClient *objstore.Client) (*TencentMPSInputPreparer, error) {
	bucket := strings.TrimSpace(cfg.InputBucket)
	if bucket == "" {
		bucket = strings.TrimSpace(cfg.OutputBucket)
	}
	if bucket == "" || strings.TrimSpace(cfg.SecretID) == "" || strings.TrimSpace(cfg.SecretKey) == "" {
		return nil, fmt.Errorf("mps input COS requires bucket and credentials")
	}
	region := strings.TrimSpace(cfg.InputRegion)
	if region == "" {
		region = strings.TrimSpace(cfg.OutputRegion)
	}
	if region == "" {
		region = strings.TrimSpace(cfg.Region)
	}
	bucketURL, err := url.Parse(fmt.Sprintf("https://%s.cos.%s.myqcloud.com", bucket, region))
	if err != nil {
		return nil, fmt.Errorf("parse mps input COS URL: %w", err)
	}
	httpClient := &http.Client{Transport: &cos.AuthorizationTransport{SecretID: cfg.SecretID, SecretKey: cfg.SecretKey, Transport: http.DefaultTransport}}
	return &TencentMPSInputPreparer{
		MinIO: minioClient, Client: cos.NewClient(&cos.BaseURL{BucketURL: bucketURL}, httpClient),
		Bucket: bucket, Region: region, Prefix: strings.Trim(cfg.InputDir, "/") + "/", SecretID: cfg.SecretID, SecretKey: cfg.SecretKey,
	}, nil
}

func (p *TencentMPSInputPreparer) Prepare(ctx context.Context, video *model.Video, sourceURL string) (string, error) {
	if video == nil || strings.TrimSpace(video.ID) == "" {
		return "", fmt.Errorf("video is missing")
	}
	if !needsMPSInputStaging(sourceURL) {
		return strings.TrimSpace(sourceURL), nil
	}
	if p == nil || p.Client == nil || p.MinIO == nil {
		return "", fmt.Errorf("mps input preparer is not configured")
	}
	objectKey := fmt.Sprintf("%s%s/source.mp4", p.Prefix, video.ID)
	_, headErr := p.Client.Object.Head(ctx, objectKey, nil)
	if headErr != nil && !cos.IsNotFoundError(headErr) {
		return "", fmt.Errorf("check mps input object: %w", headErr)
	}
	if cos.IsNotFoundError(headErr) {
		reader, size, closeFn, openErr := p.openSource(ctx, video)
		if openErr != nil {
			return "", openErr
		}
		defer closeFn()
		if _, putErr := p.Client.Object.Put(ctx, objectKey, reader, &cos.ObjectPutOptions{ObjectPutHeaderOptions: &cos.ObjectPutHeaderOptions{ContentType: "video/mp4", ContentLength: size}}); putErr != nil {
			return "", fmt.Errorf("upload mps input to COS: %w", putErr)
		}
	}
	signed, err := p.Client.Object.GetPresignedURL(ctx, http.MethodGet, objectKey, p.SecretID, p.SecretKey, mpsInputURLTTL, nil, false)
	if err != nil {
		return "", fmt.Errorf("sign mps input URL: %w", err)
	}
	return signed.String(), nil
}

func (p *TencentMPSInputPreparer) openSource(ctx context.Context, video *model.Video) (io.Reader, int64, func(), error) {
	key := videoObjectKey(video.ID, video.UploadObjectKey, video.FileURL)
	if p.MinIO.IsLocal() {
		file, err := p.MinIO.ServeLocalObject(key)
		if err != nil {
			return nil, 0, func() {}, fmt.Errorf("open local source object: %w", err)
		}
		stat, err := file.Stat()
		if err != nil {
			_ = file.Close()
			return nil, 0, func() {}, fmt.Errorf("stat local source object: %w", err)
		}
		return file, stat.Size(), func() { _ = file.Close() }, nil
	}
	signed, err := p.MinIO.PresignGet(ctx, key, 30*time.Minute)
	if err != nil {
		return nil, 0, func() {}, fmt.Errorf("sign source object: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, signed, nil)
	if err != nil {
		return nil, 0, func() {}, fmt.Errorf("create source request: %w", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, 0, func() {}, fmt.Errorf("read source object: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_ = resp.Body.Close()
		return nil, 0, func() {}, fmt.Errorf("read source object: status %s", resp.Status)
	}
	return resp.Body, resp.ContentLength, func() { _ = resp.Body.Close() }, nil
}

func needsMPSInputStaging(raw string) bool {
	raw = strings.TrimSpace(raw)
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" {
		return true
	}
	if strings.EqualFold(u.Scheme, "https") {
		return false
	}
	host := strings.ToLower(u.Hostname())
	if host == "localhost" || host == "host.docker.internal" || strings.HasSuffix(host, ".local") ||
		host == "minio" || host == "weknora-minio" || host == "custom-backend" || host == "app" {
		return true
	}
	if parsed := net.ParseIP(host); parsed != nil {
		return parsed.IsLoopback() || parsed.IsPrivate()
	}
	return true
}
