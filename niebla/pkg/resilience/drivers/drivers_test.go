package drivers

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestFilesystemDriver(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "niebla-fs-driver-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	driver, err := NewFilesystemDriver(FilesystemConfig{
		RootPath:      tempDir,
		BufferSizeMB:  1,
		AutoCreateDir: true,
	})
	if err != nil {
		t.Fatalf("failed to initialize FilesystemDriver: %v", err)
	}

	if driver.Name() != "filesystem" {
		t.Fatalf("expected driver name 'filesystem', got %s", driver.Name())
	}

	ctx := context.Background()

	// 1. Path traversal check
	err = driver.Upload(ctx, "../escape.nieblabak", strings.NewReader("bad"), 3)
	if err == nil {
		t.Fatalf("expected path traversal upload to error, but succeeded")
	}

	// 2. Upload
	data := []byte("niebla backup test payload 1234567890")
	relPath := "backups/2026-10-25/snapshot-01.nieblabak"
	if err := driver.Upload(ctx, relPath, bytes.NewReader(data), int64(len(data))); err != nil {
		t.Fatalf("failed to upload: %v", err)
	}

	// 3. Download
	rc, err := driver.Download(ctx, relPath)
	if err != nil {
		t.Fatalf("failed to download: %v", err)
	}
	defer rc.Close()

	downloaded, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("failed to read downloaded content: %v", err)
	}
	if !bytes.Equal(downloaded, data) {
		t.Fatalf("downloaded content mismatch: got %s, want %s", string(downloaded), string(data))
	}

	// 4. List
	items, err := driver.List(ctx, "backups")
	if err != nil {
		t.Fatalf("failed to list items: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	if !strings.HasSuffix(items[0].Name, "snapshot-01.nieblabak") {
		t.Fatalf("unexpected listed item name: %s", items[0].Name)
	}
	if items[0].Size != int64(len(data)) {
		t.Fatalf("expected size %d, got %d", len(data), items[0].Size)
	}

	// 5. Delete
	if err := driver.Delete(ctx, relPath); err != nil {
		t.Fatalf("failed to delete item: %v", err)
	}

	// Download should now fail
	if _, err := driver.Download(ctx, relPath); err == nil {
		t.Fatalf("expected download of deleted item to fail, but succeeded")
	}
}

func TestNextcloudDriver(t *testing.T) {
	uploadedData := make(map[string][]byte)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if !ok || user != "testuser" || pass != "testpass" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}

		switch r.Method {
		case "MKCOL":
			w.WriteHeader(http.StatusCreated)
		case http.MethodPut:
			body, _ := io.ReadAll(r.Body)
			uploadedData[r.URL.Path] = body
			w.WriteHeader(http.StatusCreated)
		case http.MethodGet:
			data, found := uploadedData[r.URL.Path]
			if !found {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			w.WriteHeader(http.StatusOK)
			w.Write(data)
		case "PROPFIND":
			xmlResponse := `<?xml version="1.0" encoding="utf-8"?>
<d:multistatus xmlns:d="DAV:">
  <d:response>
    <d:href>/remote.php/dav/files/testuser/backups/test.nieblabak</d:href>
    <d:propstat>
      <d:prop>
        <d:getcontentlength>25</d:getcontentlength>
        <d:getlastmodified>Sun, 25 Oct 2026 12:00:00 GMT</d:getlastmodified>
      </d:prop>
      <d:status>HTTP/1.1 200 OK</d:status>
    </d:propstat>
  </d:response>
</d:multistatus>`
			w.Header().Set("Content-Type", "application/xml")
			w.WriteHeader(http.StatusMultiStatus)
			w.Write([]byte(xmlResponse))
		case http.MethodDelete:
			delete(uploadedData, r.URL.Path)
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	defer ts.Close()

	driver, err := NewNextcloudDriver(NextcloudConfig{
		BaseURL:  ts.URL + "/remote.php/dav/files/testuser",
		Username: "testuser",
		Password: "testpass",
		Client:   ts.Client(),
	})
	if err != nil {
		t.Fatalf("failed to init Nextcloud driver: %v", err)
	}

	if driver.Name() != "nextcloud" {
		t.Fatalf("expected driver name 'nextcloud', got %s", driver.Name())
	}

	ctx := context.Background()
	payload := []byte("nextcloud backup contents")

	// 1. Upload
	if err := driver.Upload(ctx, "backups/test.nieblabak", bytes.NewReader(payload), int64(len(payload))); err != nil {
		t.Fatalf("failed to upload to Nextcloud: %v", err)
	}

	// 2. Download
	rc, err := driver.Download(ctx, "backups/test.nieblabak")
	if err != nil {
		t.Fatalf("failed to download from Nextcloud: %v", err)
	}
	defer rc.Close()

	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("failed to read from response: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("mismatch content: got %s, want %s", string(got), string(payload))
	}

	// 3. List
	items, err := driver.List(ctx, "backups")
	if err != nil {
		t.Fatalf("failed to list items from Nextcloud: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	if !strings.HasSuffix(items[0].Name, "test.nieblabak") {
		t.Fatalf("unexpected item name: %s", items[0].Name)
	}
	if items[0].Size != 25 {
		t.Fatalf("expected size 25, got %d", items[0].Size)
	}

	// 4. Delete
	if err := driver.Delete(ctx, "backups/test.nieblabak"); err != nil {
		t.Fatalf("failed to delete from Nextcloud: %v", err)
	}
}

func TestS3Driver(t *testing.T) {
	uploadedData := make(map[string][]byte)
	var authHeaderCaptured string

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeaderCaptured = r.Header.Get("Authorization")

		switch r.Method {
		case http.MethodPut:
			body, _ := io.ReadAll(r.Body)
			uploadedData[r.URL.Path] = body
			w.WriteHeader(http.StatusOK)
		case http.MethodGet:
			if r.URL.Query().Get("list-type") == "2" {
				xmlResponse := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<ListBucketResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/">
    <Name>niebla-backups</Name>
    <Prefix>%s</Prefix>
    <Contents>
        <Key>niebla/backups/daily.nieblabak</Key>
        <LastModified>2026-10-25T12:00:00.000Z</LastModified>
        <Size>1024</Size>
    </Contents>
</ListBucketResult>`, r.URL.Query().Get("prefix"))
				w.Header().Set("Content-Type", "application/xml")
				w.WriteHeader(http.StatusOK)
				w.Write([]byte(xmlResponse))
				return
			}

			data, found := uploadedData[r.URL.Path]
			if !found {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			w.WriteHeader(http.StatusOK)
			w.Write(data)
		case http.MethodDelete:
			delete(uploadedData, r.URL.Path)
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	defer ts.Close()

	endpoint := strings.TrimPrefix(ts.URL, "http://")

	driver, err := NewS3Driver(S3Config{
		Endpoint:       endpoint,
		Bucket:         "niebla-backups",
		Region:         "us-east-1",
		Prefix:         "niebla",
		AccessKey:      "AKIAIOSFODNN7EXAMPLE",
		SecretKey:      "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
		ForcePathStyle: true,
		UseSSL:         false,
		Client:         ts.Client(),
	})
	if err != nil {
		t.Fatalf("failed to init S3 driver: %v", err)
	}

	if driver.Name() != "s3" {
		t.Fatalf("expected driver name 's3', got %s", driver.Name())
	}

	ctx := context.Background()
	payload := []byte("s3 encrypted backup blob")

	// 1. Upload
	if err := driver.Upload(ctx, "backups/daily.nieblabak", bytes.NewReader(payload), int64(len(payload))); err != nil {
		t.Fatalf("failed to upload to S3: %v", err)
	}

	// Check that SigV4 Authorization header was generated
	if !strings.HasPrefix(authHeaderCaptured, "AWS4-HMAC-SHA256 Credential=AKIAIOSFODNN7EXAMPLE/") {
		t.Fatalf("invalid SigV4 authorization header: %s", authHeaderCaptured)
	}

	// 2. Download
	rc, err := driver.Download(ctx, "backups/daily.nieblabak")
	if err != nil {
		t.Fatalf("failed to download from S3: %v", err)
	}
	defer rc.Close()

	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("failed to read from S3 download: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("mismatched content: got %s, want %s", string(got), string(payload))
	}

	// 3. List
	items, err := driver.List(ctx, "backups")
	if err != nil {
		t.Fatalf("failed to list items from S3: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	if items[0].Name != "backups/daily.nieblabak" {
		t.Fatalf("unexpected item name: %s", items[0].Name)
	}
	if items[0].Size != 1024 {
		t.Fatalf("expected size 1024, got %d", items[0].Size)
	}

	// 4. Delete
	if err := driver.Delete(ctx, "backups/daily.nieblabak"); err != nil {
		t.Fatalf("failed to delete from S3: %v", err)
	}
}
