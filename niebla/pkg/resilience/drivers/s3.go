package drivers

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"
)

// S3Config defines connection settings for an S3-compatible protocol object store.
type S3Config struct {
	Endpoint       string
	Bucket         string
	Region         string
	Prefix         string
	AccessKey      string
	SecretKey      string
	ForcePathStyle bool
	UseSSL         bool
	StorageClass   string
	Client         *http.Client
}

// S3Driver implements StorageDriver for AWS S3, MinIO, Cloudflare R2, Ceph, and Wasabi.
type S3Driver struct {
	endpoint       string
	bucket         string
	region         string
	prefix         string
	accessKey      string
	secretKey      string
	forcePathStyle bool
	useSSL         bool
	storageClass   string
	client         *http.Client
}

// NewS3Driver creates an S3Driver instance.
func NewS3Driver(cfg S3Config) (*S3Driver, error) {
	if cfg.Bucket == "" {
		return nil, fmt.Errorf("s3 bucket cannot be empty")
	}

	endpoint := cfg.Endpoint
	if endpoint == "" {
		endpoint = "s3.amazonaws.com"
	}
	endpoint = strings.TrimPrefix(endpoint, "https://")
	endpoint = strings.TrimPrefix(endpoint, "http://")
	endpoint = strings.TrimSuffix(endpoint, "/")

	region := cfg.Region
	if region == "" {
		region = "us-east-1"
	}

	client := cfg.Client
	if client == nil {
		client = &http.Client{Timeout: 300 * time.Second}
	}

	return &S3Driver{
		endpoint:       endpoint,
		bucket:         cfg.Bucket,
		region:         region,
		prefix:         strings.TrimPrefix(cfg.Prefix, "/"),
		accessKey:      cfg.AccessKey,
		secretKey:      cfg.SecretKey,
		forcePathStyle: cfg.ForcePathStyle,
		useSSL:         cfg.UseSSL,
		storageClass:   cfg.StorageClass,
		client:         client,
	}, nil
}

func (s *S3Driver) Name() string {
	return "s3"
}

func (s *S3Driver) objectKey(relPath string) string {
	cleanRel := path.Clean(strings.TrimPrefix(relPath, "/"))
	if s.prefix == "" {
		return cleanRel
	}
	return path.Join(s.prefix, cleanRel)
}

func (s *S3Driver) buildURL(key string, queryParams url.Values) string {
	scheme := "https"
	if !s.useSSL {
		scheme = "http"
	}

	var host, pathPart string
	if s.forcePathStyle || strings.Contains(s.endpoint, "127.0.0.1") || strings.Contains(s.endpoint, "localhost") {
		host = s.endpoint
		pathPart = "/" + s.bucket + "/" + key
	} else {
		host = s.bucket + "." + s.endpoint
		pathPart = "/" + key
	}

	targetURL := &url.URL{
		Scheme:   scheme,
		Host:     host,
		Path:     pathPart,
		RawQuery: queryParams.Encode(),
	}
	return targetURL.String()
}

// signV4 signs an HTTP request with AWS Signature Version 4.
func (s *S3Driver) signV4(req *http.Request, payloadSHA256 string) {
	now := time.Now().UTC()
	dateStamp := now.Format("20060102")
	amzDate := now.Format("20060102T150405Z")

	req.Header.Set("X-Amz-Date", amzDate)
	req.Header.Set("x-amz-content-sha256", payloadSHA256)
	if s.storageClass != "" && req.Method == http.MethodPut {
		req.Header.Set("x-amz-storage-class", s.storageClass)
	}

	canonicalURI := req.URL.Path
	if canonicalURI == "" {
		canonicalURI = "/"
	}
	canonicalQuery := req.URL.RawQuery

	signedHeaders := "host;x-amz-content-sha256;x-amz-date"
	canonicalHeaders := fmt.Sprintf("host:%s\nx-amz-content-sha256:%s\nx-amz-date:%s\n",
		req.URL.Host, payloadSHA256, amzDate)

	canonicalRequest := fmt.Sprintf("%s\n%s\n%s\n%s\n%s\n%s",
		req.Method, canonicalURI, canonicalQuery, canonicalHeaders, signedHeaders, payloadSHA256)

	reqHash := sha256.Sum256([]byte(canonicalRequest))

	scope := fmt.Sprintf("%s/%s/s3/aws4_request", dateStamp, s.region)
	stringToSign := fmt.Sprintf("AWS4-HMAC-SHA256\n%s\n%s\n%s",
		amzDate, scope, hex.EncodeToString(reqHash[:]))

	// Key derivation
	kDate := hmacSHA256([]byte("AWS4"+s.secretKey), []byte(dateStamp))
	kRegion := hmacSHA256(kDate, []byte(s.region))
	kService := hmacSHA256(kRegion, []byte("s3"))
	kSigning := hmacSHA256(kService, []byte("aws4_request"))

	signature := hex.EncodeToString(hmacSHA256(kSigning, []byte(stringToSign)))

	authHeader := fmt.Sprintf("AWS4-HMAC-SHA256 Credential=%s/%s, SignedHeaders=%s, Signature=%s",
		s.accessKey, scope, signedHeaders, signature)
	req.Header.Set("Authorization", authHeader)
}

func hmacSHA256(key, data []byte) []byte {
	h := hmac.New(sha256.New, key)
	h.Write(data)
	return h.Sum(nil)
}

// Upload streams a backup file to the S3 bucket using HTTP PUT.
func (s *S3Driver) Upload(ctx context.Context, relativePath string, r io.Reader, size int64) error {
	key := s.objectKey(relativePath)
	targetURL := s.buildURL(key, nil)

	var payload []byte
	var err error
	if size > 0 && size < 32*1024*1024 {
		payload, err = io.ReadAll(r)
		if err != nil {
			return fmt.Errorf("failed to read payload for S3 upload: %w", err)
		}
		r = bytes.NewReader(payload)
	}

	payloadHash := "UNSIGNED-PAYLOAD"
	if payload != nil {
		h := sha256.Sum256(payload)
		payloadHash = hex.EncodeToString(h[:])
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, targetURL, r)
	if err != nil {
		return fmt.Errorf("failed to create S3 PUT request: %w", err)
	}

	if size > 0 {
		req.ContentLength = size
	}
	req.Header.Set("Content-Type", "application/octet-stream")

	s.signV4(req, payloadHash)

	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("S3 PUT %s failed: %w", targetURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("S3 upload failed with status %d: %s", resp.StatusCode, string(body))
	}

	return nil
}

// Download downloads an object from S3.
func (s *S3Driver) Download(ctx context.Context, relativePath string) (io.ReadCloser, error) {
	key := s.objectKey(relativePath)
	targetURL := s.buildURL(key, nil)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create S3 GET request: %w", err)
	}

	s.signV4(req, "UNSIGNED-PAYLOAD")

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("S3 GET %s failed: %w", targetURL, err)
	}

	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		return nil, fmt.Errorf("S3 GET %s failed with status %d", targetURL, resp.StatusCode)
	}

	return resp.Body, nil
}

type listBucketResultV2 struct {
	XMLName  xml.Name `xml:"ListBucketResult"`
	Contents []struct {
		Key          string    `xml:"Key"`
		LastModified time.Time `xml:"LastModified"`
		Size         int64     `xml:"Size"`
	} `xml:"Contents"`
}

// List queries objects matching the prefix using S3 ListObjectsV2.
func (s *S3Driver) List(ctx context.Context, prefix string) ([]ItemInfo, error) {
	targetPrefix := s.objectKey(prefix)
	query := url.Values{}
	query.Set("list-type", "2")
	if targetPrefix != "" {
		query.Set("prefix", targetPrefix)
	}

	targetURL := s.buildURL("", query)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create S3 ListObjects request: %w", err)
	}

	s.signV4(req, "UNSIGNED-PAYLOAD")

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("S3 ListObjects failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("S3 ListObjects returned status %d", resp.StatusCode)
	}

	var res listBucketResultV2
	if err := xml.NewDecoder(resp.Body).Decode(&res); err != nil {
		return nil, fmt.Errorf("failed to decode S3 ListBucket XML: %w", err)
	}

	var items []ItemInfo
	for _, item := range res.Contents {
		relKey := item.Key
		if s.prefix != "" && strings.HasPrefix(relKey, s.prefix) {
			relKey = strings.TrimPrefix(relKey, s.prefix)
			relKey = strings.TrimPrefix(relKey, "/")
		}
		items = append(items, ItemInfo{
			Name:    relKey,
			Size:    item.Size,
			ModTime: item.LastModified.UTC(),
		})
	}

	return items, nil
}

// Delete removes an object from S3.
func (s *S3Driver) Delete(ctx context.Context, relativePath string) error {
	key := s.objectKey(relativePath)
	targetURL := s.buildURL(key, nil)

	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, targetURL, nil)
	if err != nil {
		return fmt.Errorf("failed to create S3 DELETE request: %w", err)
	}

	s.signV4(req, "UNSIGNED-PAYLOAD")

	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("S3 DELETE %s failed: %w", targetURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusNotFound {
		return fmt.Errorf("S3 DELETE %s returned status %d", targetURL, resp.StatusCode)
	}

	return nil
}
