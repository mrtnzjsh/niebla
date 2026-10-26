package drivers

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"
)

// NextcloudConfig configures the WebDAV driver for Nextcloud storage.
type NextcloudConfig struct {
	BaseURL  string
	Username string
	Password string
	Timeout  time.Duration
	Client   *http.Client
}

// NextcloudDriver interacts with Nextcloud via WebDAV.
type NextcloudDriver struct {
	baseURL  string
	username string
	password string
	client   *http.Client
}

// NewNextcloudDriver initializes a new Nextcloud WebDAV driver.
func NewNextcloudDriver(cfg NextcloudConfig) (*NextcloudDriver, error) {
	if cfg.BaseURL == "" {
		return nil, fmt.Errorf("nextcloud url cannot be empty")
	}
	if cfg.Username == "" {
		return nil, fmt.Errorf("nextcloud username cannot be empty")
	}

	client := cfg.Client
	if client == nil {
		timeout := cfg.Timeout
		if timeout <= 0 {
			timeout = 180 * time.Second
		}
		client = &http.Client{Timeout: timeout}
	}

	return &NextcloudDriver{
		baseURL:  strings.TrimSuffix(cfg.BaseURL, "/"),
		username: cfg.Username,
		password: cfg.Password,
		client:   client,
	}, nil
}

func (n *NextcloudDriver) Name() string {
	return "nextcloud"
}

func (n *NextcloudDriver) fullURL(relativePath string) string {
	cleanRel := path.Clean(strings.TrimPrefix(relativePath, "/"))
	if cleanRel == "." || cleanRel == "" {
		return n.baseURL
	}
	return n.baseURL + "/" + cleanRel
}

func (n *NextcloudDriver) setAuth(req *http.Request) {
	if n.username != "" {
		req.SetBasicAuth(n.username, n.password)
	}
	req.Header.Set("User-Agent", "Niebla-Resilience/1.0")
}

// EnsureDirectory creates any missing directory hierarchy using WebDAV MKCOL.
func (n *NextcloudDriver) EnsureDirectory(ctx context.Context, relativeDir string) error {
	cleanDir := path.Clean(strings.TrimPrefix(relativeDir, "/"))
	if cleanDir == "." || cleanDir == "" {
		return nil
	}

	segments := strings.Split(cleanDir, "/")
	current := ""

	for _, seg := range segments {
		if seg == "" {
			continue
		}
		current = path.Join(current, seg)
		targetURL := n.fullURL(current)

		req, err := http.NewRequestWithContext(ctx, "MKCOL", targetURL, nil)
		if err != nil {
			return err
		}
		n.setAuth(req)

		resp, err := n.client.Do(req)
		if err != nil {
			return fmt.Errorf("failed to MKCOL directory %s: %w", targetURL, err)
		}
		_ = resp.Body.Close()

		// 201 Created or 405 Method Not Allowed (already exists) are expected
		if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusMethodNotAllowed && resp.StatusCode != http.StatusOK {
			return fmt.Errorf("MKCOL %s returned status %d", targetURL, resp.StatusCode)
		}
	}

	return nil
}

// Upload sends data via HTTP PUT directly to Nextcloud storage.
func (n *NextcloudDriver) Upload(ctx context.Context, relativePath string, r io.Reader, size int64) error {
	dir := path.Dir(relativePath)
	if dir != "." && dir != "/" {
		if err := n.EnsureDirectory(ctx, dir); err != nil {
			return fmt.Errorf("failed to ensure parent directories: %w", err)
		}
	}

	targetURL := n.fullURL(relativePath)
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, targetURL, r)
	if err != nil {
		return fmt.Errorf("failed to construct PUT request: %w", err)
	}
	n.setAuth(req)

	if size > 0 {
		req.ContentLength = size
	}
	req.Header.Set("Content-Type", "application/octet-stream")

	resp, err := n.client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to upload to Nextcloud: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("upload to %s failed with status %d: %s", targetURL, resp.StatusCode, string(body))
	}

	return nil
}

// Download fetches an item via HTTP GET.
func (n *NextcloudDriver) Download(ctx context.Context, relativePath string) (io.ReadCloser, error) {
	targetURL := n.fullURL(relativePath)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to construct GET request: %w", err)
	}
	n.setAuth(req)

	resp, err := n.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to download from Nextcloud: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		return nil, fmt.Errorf("download from %s failed with status %d", targetURL, resp.StatusCode)
	}

	return resp.Body, nil
}

// XML structs for WebDAV PROPFIND responses
type propfindResponse struct {
	XMLName   xml.Name `xml:"response"`
	Href      string   `xml:"href"`
	PropStat  propStat `xml:"propstat"`
}

type propStat struct {
	Prop prop `xml:"prop"`
}

type prop struct {
	GetContentLength int64  `xml:"getcontentlength"`
	GetLastModified  string `xml:"getlastmodified"`
	ResourceType     resType `xml:"resourcetype"`
}

type resType struct {
	Collection *struct{} `xml:"collection"`
}

type multiStatus struct {
	XMLName   xml.Name           `xml:"multistatus"`
	Responses []propfindResponse `xml:"response"`
}

// List queries folder contents via WebDAV PROPFIND.
func (n *NextcloudDriver) List(ctx context.Context, prefix string) ([]ItemInfo, error) {
	targetURL := n.fullURL(prefix)
	req, err := http.NewRequestWithContext(ctx, "PROPFIND", targetURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to construct PROPFIND request: %w", err)
	}
	n.setAuth(req)
	req.Header.Set("Depth", "1")

	resp, err := n.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("PROPFIND failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if resp.StatusCode != 207 && resp.StatusCode != 200 {
		return nil, fmt.Errorf("PROPFIND returned status %d", resp.StatusCode)
	}

	var ms multiStatus
	if err := xml.NewDecoder(resp.Body).Decode(&ms); err != nil {
		return nil, fmt.Errorf("failed to decode WebDAV XML multi-status: %w", err)
	}

	var items []ItemInfo
	parsedBase, _ := url.Parse(n.baseURL)

	for _, r := range ms.Responses {
		// Ignore directories/collections
		if r.PropStat.Prop.ResourceType.Collection != nil {
			continue
		}

		itemPath := r.Href
		if parsedBase != nil && strings.HasPrefix(itemPath, parsedBase.Path) {
			itemPath = strings.TrimPrefix(itemPath, parsedBase.Path)
		}
		itemPath = strings.TrimPrefix(itemPath, "/")

		modTime, _ := time.Parse(time.RFC1123, r.PropStat.Prop.GetLastModified)

		items = append(items, ItemInfo{
			Name:    itemPath,
			Size:    r.PropStat.Prop.GetContentLength,
			ModTime: modTime.UTC(),
		})
	}

	return items, nil
}

// Delete removes an item via HTTP DELETE.
func (n *NextcloudDriver) Delete(ctx context.Context, relativePath string) error {
	targetURL := n.fullURL(relativePath)
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, targetURL, nil)
	if err != nil {
		return fmt.Errorf("failed to construct DELETE request: %w", err)
	}
	n.setAuth(req)

	resp, err := n.client.Do(req)
	if err != nil {
		return fmt.Errorf("DELETE request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusNotFound {
		return fmt.Errorf("DELETE %s failed with status %d", targetURL, resp.StatusCode)
	}

	return nil
}
