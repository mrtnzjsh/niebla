package handlers

import (
	"encoding/hex"
	"io"
	"net/http"
	"strconv"

	"niebla/internal/db"
	"niebla/pkg/cas"
)

// RawHandler handles serving raw blobs from CAS.
type RawHandler struct {
	db  *db.DB
	cas *cas.Storage
}

// NewRawHandler initializes a new RawHandler.
func NewRawHandler(database *db.DB, casStorage *cas.Storage) *RawHandler {
	return &RawHandler{
		db:  database,
		cas: casStorage,
	}
}

// ServeRawBlob streams raw blob contents from CAS given a hex hash parameter.
func (h *RawHandler) ServeRawBlob(w http.ResponseWriter, r *http.Request) {
	hashHex := r.URL.Query().Get("hash")
	if hashHex == "" {
		http.Error(w, "missing 'hash' query parameter", http.StatusBadRequest)
		return
	}

	hashBytes, err := hex.DecodeString(hashHex)
	if err != nil || len(hashBytes) != 32 {
		http.Error(w, "invalid sha256 hash hex string", http.StatusBadRequest)
		return
	}

	file, size, err := h.cas.OpenBlob(hashBytes)
	if err != nil {
		http.Error(w, "blob not found in CAS storage", http.StatusNotFound)
		return
	}
	defer file.Close()

	// Read small snippet to detect content type
	buf := make([]byte, 512)
	n, _ := file.Read(buf)
	_, _ = file.Seek(0, io.SeekStart)

	contentType := "text/plain; charset=utf-8"
	if n > 0 {
		detected := http.DetectContentType(buf[:n])
		if detected != "application/octet-stream" {
			contentType = detected
		}
	}

	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
	w.WriteHeader(http.StatusOK)

	_, _ = io.Copy(w, file)
}
