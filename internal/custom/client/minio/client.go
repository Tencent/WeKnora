// Package minio 提供对象存储客户端（presigned 直传 + 分片上传 + 公开 URL）。
//
// 本地验收支持两种后端：
//   - minio: 真实 MinIO 服务器
//   - local:  本机落盘 + 8090 自托管分片/文件接口
package minio

import (
	"bytes"
	"context"
	"crypto/md5"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	"github.com/Tencent/WeKnora/internal/custom/config"
)

type Mode string

const (
	ModeMinIO Mode = "minio"
	ModeLocal Mode = "local"
)

// Client 封装对象存储访问。
type Client struct {
	mode        Mode
	core        *minio.Core
	publicCore  *minio.Core
	uploadCore  *minio.Core
	bucket      string
	publicURL   string
	internalURL string
	storageDir  string
}

// New 构造对象存储客户端。
func New(cfg config.MinIOConfig) (*Client, error) {
	if strings.EqualFold(cfg.Backend, string(ModeLocal)) {
		storageDir := strings.TrimSpace(cfg.LocalDir)
		if storageDir == "" {
			storageDir = filepath.Join(os.TempDir(), "weknora-custom-storage")
		}
		if err := os.MkdirAll(storageDir, 0o755); err != nil {
			return nil, fmt.Errorf("create local storage dir: %w", err)
		}
		publicURL := strings.TrimRight(strings.TrimSpace(cfg.PublicURL), "/")
		if publicURL == "" {
			publicURL = "http://127.0.0.1:8090/api/custom/files"
		}
		return &Client{
			mode:       ModeLocal,
			bucket:     cfg.Bucket,
			publicURL:  publicURL,
			storageDir: storageDir,
		}, nil
	}

	core, err := minio.NewCore(cfg.Endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, ""),
		Secure: cfg.UseSSL,
	})
	if err != nil {
		return nil, fmt.Errorf("minio core new: %w", err)
	}
	scheme := "http"
	if cfg.UseSSL {
		scheme = "https"
	}

	// 解析 PublicURL 的 host，用于 presign（若未配 PublicURL 则回退内部 endpoint）
	publicEndpoint := cfg.Endpoint
	publicSecure := cfg.UseSSL
	if cfg.PublicURL != "" {
		if u, err := url.Parse(cfg.PublicURL); err == nil && u.Host != "" {
			publicEndpoint = u.Host
			publicSecure = u.Scheme == "https"
		}
	}
	publicCore, err := minio.NewCore(publicEndpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, ""),
		Secure: publicSecure,
		Region: "us-east-1",
	})
	if err != nil {
		return nil, fmt.Errorf("minio public core new: %w", err)
	}
	var uploadCore *minio.Core
	if uploadURL := strings.TrimSpace(cfg.UploadURL); uploadURL != "" {
		u, parseErr := url.Parse(uploadURL)
		if parseErr != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
			return nil, fmt.Errorf("invalid MINIO_UPLOAD_URL: %q", cfg.UploadURL)
		}
		uploadCore, err = minio.NewCore(u.Host, &minio.Options{
			Creds:  credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, ""),
			Secure: u.Scheme == "https",
			Region: "us-east-1",
		})
		if err != nil {
			return nil, fmt.Errorf("minio upload core new: %w", err)
		}
	}

	return &Client{
		mode:        ModeMinIO,
		core:        core,
		publicCore:  publicCore,
		uploadCore:  uploadCore,
		bucket:      cfg.Bucket,
		publicURL:   strings.TrimRight(cfg.PublicURL, "/"),
		internalURL: fmt.Sprintf("%s://%s", scheme, cfg.Endpoint),
	}, nil
}

// IsLocal reports whether the client is using local storage.
func (c *Client) IsLocal() bool { return c != nil && c.mode == ModeLocal }

// PresignResult presigned PUT 返回结果
type PresignResult struct {
	URL       string            `json:"url"`
	ObjectKey string            `json:"object_key"`
	Headers   map[string]string `json:"headers,omitempty"`
	ExpiresAt time.Time         `json:"expires_at"`
}

// MultipartHandle 分片上传句柄
type MultipartHandle struct {
	UploadID  string `json:"upload_id"`
	ObjectKey string `json:"object_key"`
}

// CompletePart 提交分片（前端上传完成后回传 ETags）
type CompletePart struct {
	PartNumber int    `json:"part_number"`
	ETag       string `json:"etag"`
}

// ErrInvalidMultipartParts indicates the client submitted malformed multipart
// completion metadata before the storage backend was called.
var ErrInvalidMultipartParts = errors.New("invalid multipart parts")

// ErrMultipartRequestRead identifies a client/proxy connection that ended
// while the backend was reading a part body.
var ErrMultipartRequestRead = errors.New("multipart request body read failed")

// ErrMultipartStorageWrite identifies a failure while persisting a part in
// local storage or MinIO.
var ErrMultipartStorageWrite = errors.New("multipart storage write failed")

// ErrBrowserDirectUploadUnavailable indicates that the deployment has no
// public S3-compatible endpoint for browser uploads.
var ErrBrowserDirectUploadUnavailable = errors.New("browser direct upload endpoint is not configured")

// PresignPut 生成单次 PUT presigned URL（VP-T001）
func (c *Client) PresignPut(ctx context.Context, objectKey string, ttl time.Duration) (*PresignResult, error) {
	if ttl <= 0 {
		ttl = 15 * time.Minute
	}
	if c.IsLocal() {
		return &PresignResult{
			URL:       c.PublicURL(objectKey),
			ObjectKey: objectKey,
			ExpiresAt: time.Now().Add(ttl),
		}, nil
	}
	signed, err := c.publicCore.PresignedPutObject(ctx, c.bucket, objectKey, ttl)
	if err != nil {
		return nil, fmt.Errorf("presign put: %w", err)
	}
	return &PresignResult{
		URL:       signed.String(),
		ObjectKey: objectKey,
		ExpiresAt: time.Now().Add(ttl),
	}, nil
}

// InitiateMultipartUpload 初始化分片上传，返回 upload_id
func (c *Client) InitiateMultipartUpload(ctx context.Context, objectKey, contentType string) (*MultipartHandle, error) {
	if c.bucket == "" {
		return nil, errors.New("minio bucket 未配置")
	}
	if c.IsLocal() {
		uploadID := newUploadID()
		meta := multipartMeta{
			ObjectKey:   objectKey,
			ContentType: contentType,
			CreatedAt:   time.Now().UTC(),
		}
		if err := c.writeMultipartMeta(uploadID, meta); err != nil {
			return nil, err
		}
		return &MultipartHandle{UploadID: uploadID, ObjectKey: objectKey}, nil
	}
	uid, err := c.core.NewMultipartUpload(ctx, c.bucket, objectKey, minio.PutObjectOptions{
		ContentType: contentType,
	})
	if err != nil {
		return nil, fmt.Errorf("init multipart: %w", err)
	}
	return &MultipartHandle{UploadID: uid, ObjectKey: objectKey}, nil
}

// PresignPart 给单分片签 URL（VP-T002）
func (c *Client) PresignPart(ctx context.Context, objectKey, uploadID string, partNumber int, ttl time.Duration) (string, error) {
	if ttl <= 0 {
		ttl = 1 * time.Hour
	}
	if c.IsLocal() {
		return "", ErrBrowserDirectUploadUnavailable
	}
	if c.uploadCore == nil {
		return "", ErrBrowserDirectUploadUnavailable
	}
	reqParams := url.Values{
		"partNumber": []string{fmt.Sprintf("%d", partNumber)},
		"uploadId":   []string{uploadID},
	}
	signed, err := c.uploadCore.PresignHeader(ctx, http.MethodPut, c.bucket, objectKey, ttl, reqParams, nil)
	if err != nil {
		return "", fmt.Errorf("presign part: %w", err)
	}
	return signed.String(), nil
}

// BrowserDirectUploadAvailable reports whether the browser can write parts
// directly to the object store instead of streaming them through the backend.
func (c *Client) BrowserDirectUploadAvailable() bool {
	return c != nil && !c.IsLocal() && c.uploadCore != nil
}

// UploadMultipartPart 通过服务端连接 MinIO 上传单个分片并返回 ETag。
// 生产环境使用该路径避免浏览器访问内部 MinIO endpoint。
func (c *Client) UploadMultipartPart(ctx context.Context, objectKey, uploadID string, partNumber int, r io.Reader, size int64) (string, error) {
	if partNumber <= 0 {
		return "", errors.New("multipart part number must be positive")
	}
	if c.IsLocal() {
		return c.WriteMultipartPart(uploadID, partNumber, r)
	}
	if size < 0 {
		data, err := io.ReadAll(r)
		if err != nil {
			return "", fmt.Errorf("%w: part %d: %v", ErrMultipartRequestRead, partNumber, err)
		}
		r = bytes.NewReader(data)
		size = int64(len(data))
	}
	info, err := c.core.PutObjectPart(ctx, c.bucket, objectKey, uploadID, partNumber, r, size, minio.PutObjectPartOptions{})
	if err != nil {
		return "", fmt.Errorf("%w: part %d: %v", ErrMultipartStorageWrite, partNumber, err)
	}
	if info.ETag == "" {
		return "", fmt.Errorf("%w: part %d: empty etag", ErrMultipartStorageWrite, partNumber)
	}
	return info.ETag, nil
}

// CompleteMultipartUpload 合并分片
func (c *Client) CompleteMultipartUpload(ctx context.Context, objectKey, uploadID string, parts []CompletePart) error {
	normalized, err := normalizeMultipartParts(parts)
	if err != nil {
		return err
	}
	if c.IsLocal() {
		return c.completeLocalMultipart(uploadID, objectKey, normalized)
	}
	completed := make([]minio.CompletePart, 0, len(normalized))
	for _, p := range normalized {
		completed = append(completed, minio.CompletePart{
			PartNumber: p.PartNumber,
			ETag:       p.ETag,
		})
	}
	_, err = c.core.CompleteMultipartUpload(ctx, c.bucket, objectKey, uploadID, completed, minio.PutObjectOptions{})
	if err != nil {
		return fmt.Errorf("complete multipart: %w", err)
	}
	return nil
}

func normalizeMultipartParts(parts []CompletePart) ([]CompletePart, error) {
	if len(parts) == 0 {
		return nil, fmt.Errorf("%w: empty parts", ErrInvalidMultipartParts)
	}

	seen := make(map[int]struct{}, len(parts))
	normalized := make([]CompletePart, 0, len(parts))
	for _, part := range parts {
		if part.PartNumber <= 0 {
			return nil, fmt.Errorf("%w: part number must be positive", ErrInvalidMultipartParts)
		}
		if _, ok := seen[part.PartNumber]; ok {
			return nil, fmt.Errorf("%w: duplicate part number %d", ErrInvalidMultipartParts, part.PartNumber)
		}
		etag := strings.Trim(strings.TrimSpace(part.ETag), `"`)
		if etag == "" {
			return nil, fmt.Errorf("%w: empty etag for part %d", ErrInvalidMultipartParts, part.PartNumber)
		}
		seen[part.PartNumber] = struct{}{}
		normalized = append(normalized, CompletePart{
			PartNumber: part.PartNumber,
			ETag:       etag,
		})
	}

	sort.Slice(normalized, func(i, j int) bool {
		return normalized[i].PartNumber < normalized[j].PartNumber
	})
	return normalized, nil
}

// AbortMultipartUpload 取消分片
func (c *Client) AbortMultipartUpload(ctx context.Context, objectKey, uploadID string) error {
	if c.IsLocal() {
		return os.RemoveAll(c.multipartUploadDir(uploadID))
	}
	if err := c.core.AbortMultipartUpload(ctx, c.bucket, objectKey, uploadID); err != nil {
		return fmt.Errorf("abort multipart: %w", err)
	}
	return nil
}

// PutObject 上传对象。
func (c *Client) PutObject(ctx context.Context, objectKey string, reader io.Reader, size int64, opts minio.PutObjectOptions) (minio.UploadInfo, error) {
	if c.IsLocal() {
		if err := c.writeLocalObject(objectKey, reader); err != nil {
			return minio.UploadInfo{}, err
		}
		return minio.UploadInfo{Key: objectKey, Bucket: c.bucket, Size: size}, nil
	}
	return c.core.Client.PutObject(ctx, c.bucket, objectKey, reader, size, opts)
}

// ObjectExists reports whether a completed object is present.
func (c *Client) ObjectExists(ctx context.Context, objectKey string) (bool, error) {
	if c == nil {
		return false, errors.New("minio client is nil")
	}
	if c.IsLocal() {
		_, err := os.Stat(c.LocalObjectPath(objectKey))
		if err == nil {
			return true, nil
		}
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	_, err := c.Raw().StatObject(ctx, c.bucket, objectKey, minio.StatObjectOptions{})
	if err == nil {
		return true, nil
	}
	resp := minio.ToErrorResponse(err)
	if resp.Code == "NoSuchKey" || resp.Code == "NoSuchObject" || resp.StatusCode == http.StatusNotFound {
		return false, nil
	}
	return false, err
}

// PublicURL 返回对象公开访问 URL
func (c *Client) PublicURL(objectKey string) string {
	if c == nil || c.publicURL == "" {
		return ""
	}
	return strings.TrimRight(c.publicURL, "/") + "/" + strings.TrimLeft(objectKey, "/")
}

// InternalURL 返回 worker 在容器网络内读取对象的地址。
func (c *Client) InternalURL(objectKey string) string {
	if c.IsLocal() {
		return c.PublicURL(objectKey)
	}
	return fmt.Sprintf("%s/%s/%s", c.internalURL, c.bucket, objectKey)
}

// PresignGet 生成 worker 可访问的对象读取 URL。
func (c *Client) PresignGet(ctx context.Context, objectKey string, ttl time.Duration) (string, error) {
	if ttl <= 0 {
		ttl = 15 * time.Minute
	}
	if c.IsLocal() {
		return c.PublicURL(objectKey), nil
	}
	value, err := c.core.Client.PresignedGetObject(ctx, c.bucket, objectKey, ttl, nil)
	if err != nil {
		return "", fmt.Errorf("presign get: %w", err)
	}
	return value.String(), nil
}

// Bucket 返回桶名
func (c *Client) Bucket() string { return c.bucket }

// Raw 返回底层 *minio.Client（仅真实 MinIO 模式可用）。
func (c *Client) Raw() *minio.Client {
	if c == nil || c.IsLocal() || c.core == nil {
		return nil
	}
	return c.core.Client
}

// OpenObject 打开对象用于 HTTP 直出；仅真实 MinIO 模式可用。
func (c *Client) OpenObject(ctx context.Context, objectKey string) (*minio.Object, error) {
	if c == nil || c.IsLocal() || c.Raw() == nil {
		return nil, errors.New("minio object stream only available in minio mode")
	}
	obj, err := c.Raw().GetObject(ctx, c.bucket, objectKey, minio.GetObjectOptions{})
	if err != nil {
		return nil, err
	}
	if _, err := obj.Stat(); err != nil {
		_ = obj.Close()
		return nil, err
	}
	return obj, nil
}

// EnsureBucket 确保业务桶存在。
func (c *Client) EnsureBucket(ctx context.Context) error {
	if c.IsLocal() {
		return os.MkdirAll(c.storageDir, 0o755)
	}
	found, err := c.Raw().BucketExists(ctx, c.bucket)
	if err != nil {
		return fmt.Errorf("check bucket %s: %w", c.bucket, err)
	}
	if found {
		return nil
	}
	if err := c.Raw().MakeBucket(ctx, c.bucket, minio.MakeBucketOptions{Region: "us-east-1"}); err != nil {
		return fmt.Errorf("create bucket %s: %w", c.bucket, err)
	}
	return nil
}

// LocalUploadPartURL 返回本机分片上传 URL。
func (c *Client) LocalUploadPartURL(uploadID string, partNumber int) string {
	return c.localMultipartPartURL(uploadID, partNumber)
}

// LocalObjectPath 返回本机对象存储路径。
func (c *Client) LocalObjectPath(objectKey string) string {
	return filepath.Join(c.storageDir, sanitizeObjectKey(objectKey))
}

// ServeLocalObject 打开本机对象文件。
func (c *Client) ServeLocalObject(objectKey string) (*os.File, error) {
	return os.Open(c.LocalObjectPath(objectKey))
}

// WriteMultipartPart 写入本机分片并返回 ETag。
func (c *Client) WriteMultipartPart(uploadID string, partNumber int, r io.Reader) (string, error) {
	partPath := c.multipartPartPath(uploadID, partNumber)
	if err := os.MkdirAll(filepath.Dir(partPath), 0o755); err != nil {
		return "", err
	}
	data, err := io.ReadAll(r)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrMultipartRequestRead, err)
	}
	if err := os.WriteFile(partPath, data, 0o644); err != nil {
		return "", fmt.Errorf("%w: %v", ErrMultipartStorageWrite, err)
	}
	sum := md5.Sum(data)
	return `"` + hex.EncodeToString(sum[:]) + `"`, nil
}

type multipartMeta struct {
	ObjectKey   string    `json:"object_key"`
	ContentType string    `json:"content_type"`
	CreatedAt   time.Time `json:"created_at"`
}

func (c *Client) localMultipartPartURL(uploadID string, partNumber int) string {
	base := c.apiBaseURL()
	if base == "" {
		return ""
	}
	return fmt.Sprintf("%s/uploads/local/%s/parts/%d", strings.TrimRight(base, "/"), uploadID, partNumber)
}

func (c *Client) apiBaseURL() string {
	if c.publicURL == "" {
		return ""
	}
	base := strings.TrimRight(c.publicURL, "/")
	if strings.HasSuffix(base, "/files") {
		return strings.TrimSuffix(base, "/files")
	}
	return base
}

func (c *Client) multipartUploadDir(uploadID string) string {
	return filepath.Join(c.storageDir, "_uploads", uploadID)
}

func (c *Client) multipartPartPath(uploadID string, partNumber int) string {
	return filepath.Join(c.multipartUploadDir(uploadID), "parts", fmt.Sprintf("%06d.part", partNumber))
}

func (c *Client) multipartMetaPath(uploadID string) string {
	return filepath.Join(c.multipartUploadDir(uploadID), "meta.json")
}

func (c *Client) writeMultipartMeta(uploadID string, meta multipartMeta) error {
	dir := c.multipartUploadDir(uploadID)
	if err := os.MkdirAll(filepath.Join(dir, "parts"), 0o755); err != nil {
		return err
	}
	buf, err := json.Marshal(meta)
	if err != nil {
		return err
	}
	return os.WriteFile(c.multipartMetaPath(uploadID), buf, 0o644)
}

func (c *Client) readMultipartMeta(uploadID string) (multipartMeta, error) {
	var meta multipartMeta
	buf, err := os.ReadFile(c.multipartMetaPath(uploadID))
	if err != nil {
		return meta, err
	}
	if err := json.Unmarshal(buf, &meta); err != nil {
		return meta, err
	}
	return meta, nil
}

func (c *Client) completeLocalMultipart(uploadID, objectKey string, parts []CompletePart) error {
	meta, err := c.readMultipartMeta(uploadID)
	if err != nil {
		return err
	}
	if meta.ObjectKey != "" && meta.ObjectKey != objectKey {
		return fmt.Errorf("object key mismatch for upload %s", uploadID)
	}
	if len(parts) == 0 {
		return errors.New("complete multipart: empty parts")
	}
	sort.Slice(parts, func(i, j int) bool { return parts[i].PartNumber < parts[j].PartNumber })
	finalPath := c.LocalObjectPath(objectKey)
	if err := os.MkdirAll(filepath.Dir(finalPath), 0o755); err != nil {
		return err
	}
	out, err := os.Create(finalPath)
	if err != nil {
		return err
	}
	defer out.Close()
	for _, part := range parts {
		partPath := c.multipartPartPath(uploadID, part.PartNumber)
		in, err := os.Open(partPath)
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, in); err != nil {
			in.Close()
			return err
		}
		in.Close()
	}
	return os.RemoveAll(c.multipartUploadDir(uploadID))
}

func (c *Client) writeLocalObject(objectKey string, reader io.Reader) error {
	path := c.LocalObjectPath(objectKey)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.Create(path)
	if err != nil {
		return err
	}
	defer tmp.Close()
	_, err = io.Copy(tmp, reader)
	return err
}

func sanitizeObjectKey(objectKey string) string {
	clean := filepath.Clean("/" + strings.TrimLeft(objectKey, "/"))
	return strings.TrimPrefix(clean, "/")
}

func newUploadID() string {
	return strings.ReplaceAll(time.Now().UTC().Format("20060102T150405.000000000"), ".", "") + "-" + randomSuffix()
}

func randomSuffix() string {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err == nil {
		return hex.EncodeToString(b)
	}
	return fmt.Sprintf("%d", time.Now().UnixNano())
}
