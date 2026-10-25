package crypto_test

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"citc-toolchain/pkg/crypto"
)

func TestAES256GCMEncryptDecrypt(t *testing.T) {
	key := make([]byte, 32)
	_, _ = io.ReadFull(rand.Reader, key)

	cipher, err := crypto.NewAES256GCM(key)
	if err != nil {
		t.Fatalf("failed to create AES-GCM cipher: %v", err)
	}

	plaintext := []byte("The quick brown fox jumps over the lazy dog. Source control resilience verified.")
	aad := []byte("test-metadata-cl-1234")

	ciphertext, nonce, err := cipher.Encrypt(plaintext, aad)
	if err != nil {
		t.Fatalf("encryption failed: %v", err)
	}

	decrypted, err := cipher.Decrypt(ciphertext, nonce, aad)
	if err != nil {
		t.Fatalf("decryption failed: %v", err)
	}

	if !bytes.Equal(plaintext, decrypted) {
		t.Fatalf("decrypted text does not match plaintext: got %s, want %s", string(decrypted), string(plaintext))
	}
}

func TestChaCha20Poly1305EncryptDecrypt(t *testing.T) {
	key := make([]byte, 32)
	_, _ = io.ReadFull(rand.Reader, key)

	cipher, err := crypto.NewChaCha20Poly1305(key)
	if err != nil {
		t.Fatalf("failed to create ChaCha cipher: %v", err)
	}

	plaintext := []byte("Testing ChaCha20-Poly1305 AEAD stream verification.")
	aad := []byte("header-data")

	ciphertext, nonce, err := cipher.Encrypt(plaintext, aad)
	if err != nil {
		t.Fatalf("encryption failed: %v", err)
	}

	decrypted, err := cipher.Decrypt(ciphertext, nonce, aad)
	if err != nil {
		t.Fatalf("decryption failed: %v", err)
	}

	if !bytes.Equal(plaintext, decrypted) {
		t.Fatalf("decrypted text does not match: got %s, want %s", string(decrypted), string(plaintext))
	}
}

func TestBitFlipTamperDetection(t *testing.T) {
	key := make([]byte, 32)
	_, _ = io.ReadFull(rand.Reader, key)

	cipher, _ := crypto.NewAES256GCM(key)
	plaintext := []byte("Mission critical code review approvals cannot be altered.")
	aad := []byte("tamper-test")

	ciphertext, nonce, err := cipher.Encrypt(plaintext, aad)
	if err != nil {
		t.Fatalf("encryption failed: %v", err)
	}

	// 1. Flip a single bit in the ciphertext payload
	tamperedCiphertext := make([]byte, len(ciphertext))
	copy(tamperedCiphertext, ciphertext)
	tamperedCiphertext[10] ^= 0x01

	_, err = cipher.Decrypt(tamperedCiphertext, nonce, aad)
	if err == nil {
		t.Fatalf("expected decryption error on tampered ciphertext, but succeeded!")
	}
	if !errors.Is(err, crypto.ErrAuthFailed) {
		t.Errorf("expected ErrAuthFailed, got %v", err)
	}

	// 2. Tamper with additional authenticated data (AAD)
	wrongAAD := []byte("tamper-test-altered")
	_, err = cipher.Decrypt(ciphertext, nonce, wrongAAD)
	if err == nil {
		t.Fatalf("expected decryption error on tampered AAD, but succeeded!")
	}
	if !errors.Is(err, crypto.ErrAuthFailed) {
		t.Errorf("expected ErrAuthFailed on altered AAD, got %v", err)
	}
}

func TestStreamEncryptDecrypt(t *testing.T) {
	key := make([]byte, 32)
	_, _ = io.ReadFull(rand.Reader, key)

	cipher, err := crypto.NewAES256GCM(key)
	if err != nil {
		t.Fatalf("failed to create cipher: %v", err)
	}

	// Create 250KB of test data (spans multiple 64KB chunks)
	dataSize := 250 * 1024
	originalData := make([]byte, dataSize)
	_, _ = io.ReadFull(rand.Reader, originalData)

	var encryptedBuf bytes.Buffer
	aad := []byte("stream-session-1")

	// Stream Encrypt
	if err := cipher.StreamEncrypt(bytes.NewReader(originalData), &encryptedBuf, aad); err != nil {
		t.Fatalf("stream encryption failed: %v", err)
	}

	// Stream Decrypt
	var decryptedBuf bytes.Buffer
	if err := cipher.StreamDecrypt(&encryptedBuf, &decryptedBuf, aad); err != nil {
		t.Fatalf("stream decryption failed: %v", err)
	}

	if !bytes.Equal(originalData, decryptedBuf.Bytes()) {
		t.Fatalf("decrypted stream does not match original data (len %d vs %d)", decryptedBuf.Len(), len(originalData))
	}
}

func TestStreamTruncationDetection(t *testing.T) {
	key := make([]byte, 32)
	_, _ = io.ReadFull(rand.Reader, key)
	cipher, _ := crypto.NewAES256GCM(key)

	// Create 200KB of test data
	originalData := make([]byte, 200*1024)
	_, _ = io.ReadFull(rand.Reader, originalData)

	var encryptedBuf bytes.Buffer
	_ = cipher.StreamEncrypt(bytes.NewReader(originalData), &encryptedBuf, nil)

	// Truncate the stream by cutting off the last 500 bytes (simulating incomplete network transfer)
	truncatedBytes := encryptedBuf.Bytes()[:encryptedBuf.Len()-500]

	var decryptedBuf bytes.Buffer
	err := cipher.StreamDecrypt(bytes.NewReader(truncatedBytes), &decryptedBuf, nil)
	if err == nil {
		t.Fatalf("expected error on truncated stream, but got nil")
	}
}

func TestKeyLoadingFromEnv(t *testing.T) {
	rawKey := make([]byte, 32)
	_, _ = io.ReadFull(rand.Reader, rawKey)
	hexKey := hex.EncodeToString(rawKey)

	_ = os.Setenv("TEST_CITC_KEY", hexKey)
	defer os.Unsetenv("TEST_CITC_KEY")

	cfg := crypto.KeyConfig{
		Source: "env",
		EnvVar: "TEST_CITC_KEY",
	}

	loadedKey, _, err := crypto.LoadMasterKey(cfg, nil)
	if err != nil {
		t.Fatalf("failed to load key from env: %v", err)
	}
	defer crypto.WipeKey(loadedKey)

	if !bytes.Equal(rawKey, loadedKey) {
		t.Fatalf("loaded key mismatch")
	}
}

func TestKeyLoadingFromFilePermissions(t *testing.T) {
	tempDir := t.TempDir()
	keyPath := filepath.Join(tempDir, "master.key")
	hexKey := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

	// 1. Insecure permissions (0666) -> Must fail
	if err := os.WriteFile(keyPath, []byte(hexKey), 0666); err != nil {
		t.Fatalf("failed to write test keyfile: %v", err)
	}

	cfg := crypto.KeyConfig{
		Source:   "file",
		FilePath: keyPath,
	}

	_, _, err := crypto.LoadMasterKey(cfg, nil)
	if err == nil {
		t.Fatalf("expected error on keyfile with 0666 permissions, got nil")
	}
	if !errors.Is(err, crypto.ErrKeyFileInsecurePerm) {
		t.Errorf("expected ErrKeyFileInsecurePerm, got %v", err)
	}

	// 2. Secure permissions (0600) -> Must succeed
	if err := os.Chmod(keyPath, 0600); err != nil {
		t.Fatalf("failed to chmod test keyfile: %v", err)
	}

	loadedKey, _, err := crypto.LoadMasterKey(cfg, nil)
	if err != nil {
		t.Fatalf("expected success with 0600 permissions, got: %v", err)
	}
	defer crypto.WipeKey(loadedKey)

	if hex.EncodeToString(loadedKey) != hexKey {
		t.Fatalf("loaded key content mismatch")
	}
}

func TestArgon2idKeyDerivation(t *testing.T) {
	passphrase := "CorrectHorseBatteryStaple-CitC-2026!"
	_ = os.Setenv("TEST_PASSPHRASE", passphrase)
	defer os.Unsetenv("TEST_PASSPHRASE")

	cfg := crypto.KeyConfig{
		Source:        "passphrase",
		PassphraseEnv: "TEST_PASSPHRASE",
	}

	// 1. Initial derivation generates random salt
	key1, salt1, err := crypto.LoadMasterKey(cfg, nil)
	if err != nil {
		t.Fatalf("Argon2id derivation failed: %v", err)
	}
	defer crypto.WipeKey(key1)

	if len(key1) != 32 {
		t.Fatalf("expected 32-byte key, got %d", len(key1))
	}
	if len(salt1) != 16 {
		t.Fatalf("expected 16-byte salt, got %d", len(salt1))
	}

	// 2. Re-derivation with same passphrase and same salt must yield identical key
	key2, salt2, err := crypto.LoadMasterKey(cfg, salt1)
	if err != nil {
		t.Fatalf("second derivation failed: %v", err)
	}
	defer crypto.WipeKey(key2)

	if !bytes.Equal(key1, key2) {
		t.Fatalf("Argon2id was not deterministic with same salt")
	}
	if !bytes.Equal(salt1, salt2) {
		t.Fatalf("salt mismatch")
	}

	// 3. Different salt yields completely different key
	differentSalt := make([]byte, 16)
	differentSalt[0] = 0xFF
	key3, _, _ := crypto.LoadMasterKey(cfg, differentSalt)
	defer crypto.WipeKey(key3)

	if bytes.Equal(key1, key3) {
		t.Fatalf("different salt produced identical key!")
	}
}

func TestEnvelopeWrapUnwrap(t *testing.T) {
	kek, err := crypto.GenerateDEK()
	if err != nil {
		t.Fatalf("failed to generate KEK: %v", err)
	}
	dek, err := crypto.GenerateDEK()
	if err != nil {
		t.Fatalf("failed to generate DEK: %v", err)
	}

	// Wrap DEK
	wrapped, err := crypto.WrapDEK(kek, dek)
	if err != nil {
		t.Fatalf("failed to wrap DEK: %v", err)
	}

	// Unwrap DEK with correct KEK
	unwrapped, err := crypto.UnwrapDEK(kek, wrapped)
	if err != nil {
		t.Fatalf("failed to unwrap DEK: %v", err)
	}

	if !bytes.Equal(dek, unwrapped) {
		t.Fatalf("unwrapped DEK does not match original DEK")
	}

	// Unwrap with wrong KEK must fail
	wrongKEK, _ := crypto.GenerateDEK()
	_, err = crypto.UnwrapDEK(wrongKEK, wrapped)
	if err == nil {
		t.Fatalf("expected unwrap to fail with wrong KEK, but succeeded")
	}
}

func TestHeaderSerialization(t *testing.T) {
	originalHeader := &crypto.EnvelopeHeader{
		Version:      1,
		CipherSuite:  crypto.SuiteAES256GCM,
		KDFMode:      crypto.KDFModeArgon2id,
		Salt:         [16]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16},
		WrappedDEK:   []byte("test-wrapped-dek-content-60bytes-length"),
		MetadataJSON: []byte(`{"snapshot_id":"citc-snap-1","cl_count":42,"total_blobs":120}`),
	}

	var buf bytes.Buffer
	if err := crypto.WriteHeader(&buf, originalHeader); err != nil {
		t.Fatalf("WriteHeader failed: %v", err)
	}

	parsedHeader, err := crypto.ReadHeader(&buf)
	if err != nil {
		t.Fatalf("ReadHeader failed: %v", err)
	}

	if parsedHeader.Version != originalHeader.Version {
		t.Errorf("version mismatch: %d vs %d", parsedHeader.Version, originalHeader.Version)
	}
	if parsedHeader.CipherSuite != originalHeader.CipherSuite {
		t.Errorf("cipher suite mismatch: %d vs %d", parsedHeader.CipherSuite, originalHeader.CipherSuite)
	}
	if parsedHeader.KDFMode != originalHeader.KDFMode {
		t.Errorf("kdf mode mismatch: %d vs %d", parsedHeader.KDFMode, originalHeader.KDFMode)
	}
	if parsedHeader.Salt != originalHeader.Salt {
		t.Errorf("salt mismatch")
	}
	if !bytes.Equal(parsedHeader.WrappedDEK, originalHeader.WrappedDEK) {
		t.Errorf("wrapped DEK mismatch")
	}
	if !bytes.Equal(parsedHeader.MetadataJSON, originalHeader.MetadataJSON) {
		t.Errorf("metadata JSON mismatch")
	}
}
