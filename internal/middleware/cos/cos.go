package cos

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/kiritosuki/GoVideo/internal/config"
	tencentcos "github.com/tencentyun/cos-go-sdk-v5"
)

const defaultTimeout = 300 * time.Second

// Client 封装腾讯云COS客户端 外部通过该对象完成对象存储操作
type Client struct {
	client        *tencentcos.Client
	bucketURL     *url.URL
	publicBaseURL string
}

// NewClient 创建COS客户端
func NewClient(cfg *config.COSConfig) (*Client, error) {
	if cfg == nil {
		return nil, errors.New("cos config is nil")
	}
	if cfg.Bucket == "" || cfg.Region == "" {
		return nil, errors.New("cos bucket and region are required")
	}
	if cfg.SecretID == "" || cfg.SecretKey == "" {
		return nil, errors.New("cos secret id and secret key are required")
	}

	bucketURL, err := url.Parse(fmt.Sprintf("https://%s.cos.%s.myqcloud.com", cfg.Bucket, cfg.Region))
	if err != nil {
		return nil, err
	}

	timeout := defaultTimeout
	if cfg.TimeoutSeconds > 0 {
		timeout = time.Duration(cfg.TimeoutSeconds) * time.Second
	}

	baseURL := &tencentcos.BaseURL{BucketURL: bucketURL}
	httpClient := &http.Client{
		Timeout: timeout,
		Transport: &tencentcos.AuthorizationTransport{
			SecretID:  cfg.SecretID,
			SecretKey: cfg.SecretKey,
		},
	}

	return &Client{
		client:        tencentcos.NewClient(baseURL, httpClient),
		bucketURL:     bucketURL,
		publicBaseURL: strings.TrimRight(strings.TrimSpace(cfg.PublicBaseURL), "/"),
	}, nil
}

// UploadFile 上传本地文件 返回对象可访问URL
// SDK会根据文件大小自动选择普通上传或分块上传
func (c *Client) UploadFile(ctx context.Context, key string, filePath string) (string, error) {
	key = normalizeKey(key)
	if c == nil || c.client == nil {
		return "", errors.New("cos client is not initialized")
	}
	if key == "" {
		return "", errors.New("cos object key is required")
	}
	if strings.TrimSpace(filePath) == "" {
		return "", errors.New("cos upload file path is required")
	}
	if _, _, err := c.client.Object.Upload(ctx, key, filePath, nil); err != nil {
		return "", err
	}
	return c.ObjectURL(key)
}

// DeleteObject 删除对象
func (c *Client) DeleteObject(ctx context.Context, key string) error {
	key = normalizeKey(key)
	if c == nil || c.client == nil {
		return errors.New("cos client is not initialized")
	}
	if key == "" {
		return errors.New("cos object key is required")
	}
	_, err := c.client.Object.Delete(ctx, key)
	return err
}

// ObjectURL 根据对象key生成可访问URL
func (c *Client) ObjectURL(key string) (string, error) {
	key = normalizeKey(key)
	if key == "" {
		return "", errors.New("cos object key is required")
	}
	if c == nil || c.bucketURL == nil {
		return "", errors.New("cos client is not initialized")
	}
	if c.publicBaseURL != "" {
		return c.publicBaseURL + "/" + key, nil
	}
	return strings.TrimRight(c.bucketURL.String(), "/") + "/" + key, nil
}

// normalizeKey 去掉key前后的空格和前导的"/"
func normalizeKey(key string) string {
	return strings.TrimLeft(strings.TrimSpace(key), "/")
}
