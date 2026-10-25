package crypto

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/crypto/argon2"
)

const (
	// Argon2id parameters (RFC 9106 recommended memory-hard profile).
	ArgonTime        uint32 = 3
	ArgonMemory      uint32 = 64 * 1024 // 64 MB
	ArgonParallelism uint8  = 4
	KeyLength        uint32 = 32 // 256 bits
	SaltLength              = 16 // 128-bit salt
)

var (
	ErrKeyNotFound         = errors.New("crypto: key source did not provide a key")
	ErrKeyFileInsecurePerm = errors.New("crypto: key file permissions are too open (must be 0400 or 0600)")
	ErrInvalidKeyLength    = errors.New("crypto: master key must be exactly 32 bytes (256 bits)")
)

// KeyConfig defines how a master key (KEK) is loaded.
type KeyConfig struct {
	Source        string // "env", "file", "passphrase"
	EnvVar        string // environment variable name when Source = "env"
	FilePath      string // file path when Source = "file"
	PassphraseEnv string // environment variable name holding passphrase when Source = "passphrase"
}

// LoadMasterKey resolves and loads the 256-bit Master Key (KEK) and returns the key and any salt used.
// If salt is provided (e.g. from an existing archive header during restore), it is used;
// otherwise, a fresh 16-byte random salt is generated for passphrase-derived keys.
func LoadMasterKey(cfg KeyConfig, existingSalt []byte) (key []byte, salt []byte, err error) {
	switch strings.ToLower(strings.TrimSpace(cfg.Source)) {
	case "env":
		if cfg.EnvVar == "" {
			return nil, nil, fmt.Errorf("%w: key_env not specified", ErrKeyNotFound)
		}
		raw := os.Getenv(cfg.EnvVar)
		if raw == "" {
			return nil, nil, fmt.Errorf("%w: environment variable %q is empty", ErrKeyNotFound, cfg.EnvVar)
		}
		keyBytes, err := DecodeKeyString(raw)
		if err != nil {
			return nil, nil, err
		}
		if len(keyBytes) != int(KeyLength) {
			return nil, nil, fmt.Errorf("%w: expected %d bytes, got %d", ErrInvalidKeyLength, KeyLength, len(keyBytes))
		}
		return keyBytes, nil, nil

	case "file":
		if cfg.FilePath == "" {
			return nil, nil, fmt.Errorf("%w: key_file not specified", ErrKeyNotFound)
		}
		info, err := os.Stat(cfg.FilePath)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to access key file %s: %w", cfg.FilePath, err)
		}

		// Security check: Verify file permissions are strictly restricted to owner
		perm := info.Mode().Perm()
		if perm&0077 != 0 {
			return nil, nil, fmt.Errorf("%w: %s has mode %04o", ErrKeyFileInsecurePerm, cfg.FilePath, perm)
		}

		content, err := os.ReadFile(cfg.FilePath)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to read key file %s: %w", cfg.FilePath, err)
		}

		keyBytes, err := DecodeKeyString(strings.TrimSpace(string(content)))
		if err != nil {
			return nil, nil, err
		}
		if len(keyBytes) != int(KeyLength) {
			return nil, nil, fmt.Errorf("%w: expected %d bytes, got %d", ErrInvalidKeyLength, KeyLength, len(keyBytes))
		}
		return keyBytes, nil, nil

	case "passphrase":
		if cfg.PassphraseEnv == "" {
			return nil, nil, fmt.Errorf("%w: passphrase_env not specified", ErrKeyNotFound)
		}
		passphrase := os.Getenv(cfg.PassphraseEnv)
		if passphrase == "" {
			return nil, nil, fmt.Errorf("%w: passphrase environment variable %q is empty", ErrKeyNotFound, cfg.PassphraseEnv)
		}

		// Use existing salt if decrypting, or generate fresh random salt if creating
		if len(existingSalt) >= SaltLength {
			salt = make([]byte, SaltLength)
			copy(salt, existingSalt[:SaltLength])
		} else {
			salt = make([]byte, SaltLength)
			if _, err := io.ReadFull(rand.Reader, salt); err != nil {
				return nil, nil, fmt.Errorf("failed to generate random salt: %w", err)
			}
		}

		// Derive 256-bit key using Argon2id
		derived := argon2.IDKey([]byte(passphrase), salt, ArgonTime, ArgonMemory, ArgonParallelism, KeyLength)
		return derived, salt, nil

	default:
		return nil, nil, fmt.Errorf("unsupported key source %q (expected 'env', 'file', or 'passphrase')", cfg.Source)
	}
}

// DecodeKeyString parses a key string that may be encoded as hex, base64, or raw 32-byte string.
func DecodeKeyString(s string) ([]byte, error) {
	s = strings.TrimSpace(s)

	// Try hex decoding (64 hex characters = 32 bytes)
	if len(s) == 64 {
		if b, err := hex.DecodeString(s); err == nil {
			return b, nil
		}
	}

	// Try base64 decoding (44 characters for 32 bytes with padding)
	if len(s) >= 42 {
		if b, err := base64.StdEncoding.DecodeString(s); err == nil && len(b) == int(KeyLength) {
			return b, nil
		}
		if b, err := base64.RawStdEncoding.DecodeString(s); err == nil && len(b) == int(KeyLength) {
			return b, nil
		}
	}

	// If exactly 32 raw bytes
	if len(s) == int(KeyLength) {
		return []byte(s), nil
	}

	return nil, fmt.Errorf("%w: provided string could not be decoded as 32-byte hex, base64, or raw key", ErrInvalidKeyLength)
}

// WipeKey zeroes key memory securely to prevent memory scraping.
func WipeKey(key []byte) {
	if len(key) == 0 {
		return
	}
	zeros := make([]byte, len(key))
	subtle.ConstantTimeCopy(1, key, zeros)
}
