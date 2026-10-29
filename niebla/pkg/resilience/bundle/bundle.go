package bundle

import (
	"archive/tar"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/klauspost/compress/zstd"
	"niebla/pkg/crypto"
)

// Manifest contains metadata describing the contents of an encrypted backup bundle.
type Manifest struct {
	BackupID     string    `json:"backup_id"`
	CreatedAt    time.Time `json:"created_at"`
	CLCount      int       `json:"cl_count"`
	BlobCount    int       `json:"blob_count"`
	TotalBytes   int64     `json:"total_bytes"`
	DatabaseName string    `json:"database_name"`
}

// PackOptions defines settings for creating an encrypted bundle.
type PackOptions struct {
	MasterKey    []byte // 32-byte Master KEK
	CipherSuite  crypto.CipherSuite
	KDFMode      uint8
	Salt         [16]byte
	Manifest     Manifest
	DBDumpReader io.Reader
	Blobs        map[string]io.Reader // sha256 hex -> reader
}

// Pack creates an authenticated, envelope-encrypted .nieblabak archive stream.
func Pack(ctx context.Context, w io.Writer, opts PackOptions) error {
	dek, err := crypto.GenerateDEK()
	if err != nil {
		return fmt.Errorf("failed to generate DEK: %w", err)
	}
	defer crypto.WipeKey(dek)

	wrappedDEK, err := crypto.WrapDEK(opts.MasterKey, dek)
	if err != nil {
		return fmt.Errorf("failed to wrap DEK: %w", err)
	}

	manifestJSON, err := json.Marshal(opts.Manifest)
	if err != nil {
		return fmt.Errorf("failed to marshal manifest: %w", err)
	}

	suite := opts.CipherSuite
	if suite == 0 {
		suite = crypto.SuiteAES256GCM
	}

	kdfMode := opts.KDFMode
	if kdfMode == 0 {
		kdfMode = crypto.KDFModeRaw
	}

	header := &crypto.EnvelopeHeader{
		Version:      crypto.CurrentEnvelopeVersion,
		CipherSuite:  suite,
		KDFMode:      kdfMode,
		Salt:         opts.Salt,
		WrappedDEK:   wrappedDEK,
		MetadataJSON: manifestJSON,
	}

	// 1. Write the cryptographic header
	if err := crypto.WriteHeader(w, header); err != nil {
		return fmt.Errorf("failed to write envelope header: %w", err)
	}

	// 2. Initialize AEAD cipher with ephemeral DEK
	var cipher crypto.AEADCipher
	if suite == crypto.SuiteChaCha20Poly1305 {
		cipher, err = crypto.NewChaCha20Poly1305(dek)
	} else {
		cipher, err = crypto.NewAES256GCM(dek)
	}
	if err != nil {
		return fmt.Errorf("failed to initialize cipher with DEK: %w", err)
	}

	// 3. Set up streaming pipeline: tar -> zstd -> cipher.StreamEncrypt -> w
	pr, pw := io.Pipe()

	encryptErrCh := make(chan error, 1)
	go func() {
		err := cipher.StreamEncrypt(pr, w, header.MetadataJSON)
		_ = pr.Close()
		encryptErrCh <- err
	}()

	zstdWriter, err := zstd.NewWriter(pw, zstd.WithEncoderLevel(zstd.SpeedFastest))
	if err != nil {
		_ = pw.Close()
		return fmt.Errorf("failed to create zstd compressor: %w", err)
	}

	tarWriter := tar.NewWriter(zstdWriter)

	// A. Write manifest.json inside tar
	manifestHeader := &tar.Header{
		Name:    "manifest.json",
		Mode:    0600,
		Size:    int64(len(manifestJSON)),
		ModTime: opts.Manifest.CreatedAt,
	}
	if err := tarWriter.WriteHeader(manifestHeader); err != nil {
		_ = tarWriter.Close()
		_ = zstdWriter.Close()
		_ = pw.Close()
		return fmt.Errorf("failed to write manifest tar header: %w", err)
	}
	if _, err := tarWriter.Write(manifestJSON); err != nil {
		_ = tarWriter.Close()
		_ = zstdWriter.Close()
		_ = pw.Close()
		return fmt.Errorf("failed to write manifest tar body: %w", err)
	}

	// B. Write database dump if provided
	if opts.DBDumpReader != nil {
		// Read dump into memory or pipe to determine size
		dumpBytes, err := io.ReadAll(opts.DBDumpReader)
		if err != nil {
			_ = tarWriter.Close()
			_ = zstdWriter.Close()
			_ = pw.Close()
			return fmt.Errorf("failed to read database dump: %w", err)
		}

		dbHeader := &tar.Header{
			Name:    "database.sql",
			Mode:    0600,
			Size:    int64(len(dumpBytes)),
			ModTime: opts.Manifest.CreatedAt,
		}
		if err := tarWriter.WriteHeader(dbHeader); err != nil {
			_ = tarWriter.Close()
			_ = zstdWriter.Close()
			_ = pw.Close()
			return fmt.Errorf("failed to write database tar header: %w", err)
		}
		if _, err := tarWriter.Write(dumpBytes); err != nil {
			_ = tarWriter.Close()
			_ = zstdWriter.Close()
			_ = pw.Close()
			return fmt.Errorf("failed to write database dump to tar: %w", err)
		}
	}

	// C. Write CAS blobs
	for sha, r := range opts.Blobs {
		blobBytes, err := io.ReadAll(r)
		if err != nil {
			_ = tarWriter.Close()
			_ = zstdWriter.Close()
			_ = pw.Close()
			return fmt.Errorf("failed to read blob %s: %w", sha, err)
		}

		bHeader := &tar.Header{
			Name:    fmt.Sprintf("blobs/%s", sha),
			Mode:    0644,
			Size:    int64(len(blobBytes)),
			ModTime: opts.Manifest.CreatedAt,
		}
		if err := tarWriter.WriteHeader(bHeader); err != nil {
			_ = tarWriter.Close()
			_ = zstdWriter.Close()
			_ = pw.Close()
			return fmt.Errorf("failed to write blob %s tar header: %w", sha, err)
		}
		if _, err := tarWriter.Write(blobBytes); err != nil {
			_ = tarWriter.Close()
			_ = zstdWriter.Close()
			_ = pw.Close()
			return fmt.Errorf("failed to write blob %s to tar: %w", sha, err)
		}
	}

	// Flush and close tar and zstd writers
	if err := tarWriter.Close(); err != nil {
		_ = zstdWriter.Close()
		_ = pw.Close()
		return fmt.Errorf("failed to close tar writer: %w", err)
	}
	if err := zstdWriter.Close(); err != nil {
		_ = pw.Close()
		return fmt.Errorf("failed to close zstd compressor: %w", err)
	}
	_ = pw.Close()

	if err := <-encryptErrCh; err != nil {
		return fmt.Errorf("stream encryption error: %w", err)
	}

	return nil
}

// UnpackResult holds decrypted and unpacked backup components.
type UnpackResult struct {
	Header   *crypto.EnvelopeHeader
	Manifest Manifest
	DBDump   []byte
	Blobs    map[string][]byte
}

// Unpack decrypts, decompresses, and reads an encrypted .nieblabak archive stream.
func Unpack(ctx context.Context, r io.Reader, masterKey []byte) (*UnpackResult, error) {
	// 1. Read envelope header
	header, err := crypto.ReadHeader(r)
	if err != nil {
		return nil, fmt.Errorf("failed to read envelope header: %w", err)
	}

	// 2. Unwrap DEK using master key
	dek, err := crypto.UnwrapDEK(masterKey, header.WrappedDEK)
	if err != nil {
		return nil, err
	}
	defer crypto.WipeKey(dek)

	// 3. Initialize AEAD cipher
	var cipher crypto.AEADCipher
	if header.CipherSuite == crypto.SuiteChaCha20Poly1305 {
		cipher, err = crypto.NewChaCha20Poly1305(dek)
	} else {
		cipher, err = crypto.NewAES256GCM(dek)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to initialize cipher with DEK: %w", err)
	}

	// 4. Stream decrypt
	pr, pw := io.Pipe()
	decryptErrCh := make(chan error, 1)
	go func() {
		err := cipher.StreamDecrypt(r, pw, header.MetadataJSON)
		_ = pw.Close()
		decryptErrCh <- err
	}()

	// 5. Decompress zstd stream
	zstdReader, err := zstd.NewReader(pr)
	if err != nil {
		_ = pr.Close()
		return nil, fmt.Errorf("failed to create zstd decompressor: %w", err)
	}
	defer zstdReader.Close()

	// 6. Read tar entries
	tarReader := tar.NewReader(zstdReader)
	result := &UnpackResult{
		Header: header,
		Blobs:  make(map[string][]byte),
	}

	for {
		entryHeader, err := tarReader.Next()
		if err != nil {
			if err == io.EOF {
				break
			}
			return nil, fmt.Errorf("failed to read tar entry: %w", err)
		}

		data, err := io.ReadAll(tarReader)
		if err != nil {
			return nil, fmt.Errorf("failed to read tar entry %s: %w", entryHeader.Name, err)
		}

		if entryHeader.Name == "manifest.json" {
			_ = json.Unmarshal(data, &result.Manifest)
		} else if entryHeader.Name == "database.sql" {
			result.DBDump = data
		} else if len(entryHeader.Name) > 6 && entryHeader.Name[:6] == "blobs/" {
			sha := entryHeader.Name[6:]
			result.Blobs[sha] = data
		}
	}

	if err := <-decryptErrCh; err != nil {
		return nil, fmt.Errorf("decryption failed: %w", err)
	}

	return result, nil
}

// VerifyHeader reads and verifies the envelope header and unwraps the DEK without unpacking the full payload.
func VerifyHeader(r io.Reader, masterKey []byte) (*crypto.EnvelopeHeader, *Manifest, error) {
	header, err := crypto.ReadHeader(r)
	if err != nil {
		return nil, nil, err
	}

	dek, err := crypto.UnwrapDEK(masterKey, header.WrappedDEK)
	if err != nil {
		return nil, nil, err
	}
	crypto.WipeKey(dek)

	var manifest Manifest
	if len(header.MetadataJSON) > 0 {
		if err := json.Unmarshal(header.MetadataJSON, &manifest); err != nil {
			return nil, nil, fmt.Errorf("failed to parse manifest JSON in header: %w", err)
		}
	}

	return header, &manifest, nil
}
