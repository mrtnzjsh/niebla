package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

var (
	// MagicBytes identifies a valid Niebla encrypted backup stream.
	MagicBytes = [8]byte{'N', 'I', 'E', 'B', 'L', 'A', 'K', 0x01}

	dekAAD = []byte("niebla-envelope-dek-v1")

	ErrInvalidMagic   = errors.New("crypto: invalid backup file magic header")
	ErrInvalidHeader  = errors.New("crypto: malformed envelope header")
	ErrUnsupportedVer = errors.New("crypto: unsupported envelope version")
)

const (
	CurrentEnvelopeVersion uint16 = 1

	KDFModeRaw      uint8 = 0x01
	KDFModeArgon2id uint8 = 0x02
)

// EnvelopeHeader defines the cryptographic framing and wrapped keying material of an archive.
type EnvelopeHeader struct {
	Version      uint16
	CipherSuite  CipherSuite
	KDFMode      uint8
	Salt         [16]byte
	WrappedDEK   []byte
	MetadataJSON []byte
}

// GenerateDEK produces an ephemeral 256-bit symmetric Data Encryption Key.
func GenerateDEK() ([]byte, error) {
	dek := make([]byte, KeyLength)
	if _, err := io.ReadFull(rand.Reader, dek); err != nil {
		return nil, fmt.Errorf("failed to generate random DEK: %w", err)
	}
	return dek, nil
}

// WrapDEK encrypts the DEK using the Master Key (KEK) via AES-256-GCM.
func WrapDEK(kek, dek []byte) ([]byte, error) {
	if len(kek) != int(KeyLength) {
		return nil, fmt.Errorf("kek must be %d bytes, got %d", KeyLength, len(kek))
	}
	if len(dek) != int(KeyLength) {
		return nil, fmt.Errorf("dek must be %d bytes, got %d", KeyLength, len(dek))
	}

	block, err := aes.NewCipher(kek)
	if err != nil {
		return nil, fmt.Errorf("failed to create cipher for key wrapping: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("failed to create GCM for key wrapping: %w", err)
	}

	nonce := make([]byte, NonceSize)
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("failed to generate wrap nonce: %w", err)
	}

	ciphertext := gcm.Seal(nil, nonce, dek, dekAAD)
	wrapped := append(nonce, ciphertext...)
	return wrapped, nil
}

// UnwrapDEK decrypts the DEK using the Master Key (KEK). Fails if KEK is wrong or data is tampered.
func UnwrapDEK(kek, wrappedDEK []byte) ([]byte, error) {
	if len(kek) != int(KeyLength) {
		return nil, fmt.Errorf("kek must be %d bytes, got %d", KeyLength, len(kek))
	}
	if len(wrappedDEK) <= NonceSize {
		return nil, fmt.Errorf("%w: wrapped DEK too short", ErrInvalidCiphertext)
	}

	block, err := aes.NewCipher(kek)
	if err != nil {
		return nil, fmt.Errorf("failed to create cipher for key unwrapping: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("failed to create GCM for key unwrapping: %w", err)
	}

	nonce := wrappedDEK[:NonceSize]
	ciphertext := wrappedDEK[NonceSize:]

	dek, err := gcm.Open(nil, nonce, ciphertext, dekAAD)
	if err != nil {
		return nil, fmt.Errorf("%w: failed to unwrap DEK (wrong master key or corrupted backup)", ErrAuthFailed)
	}

	return dek, nil
}

// WriteHeader serializes the EnvelopeHeader to the provided writer.
func WriteHeader(w io.Writer, h *EnvelopeHeader) error {
	if _, err := w.Write(MagicBytes[:]); err != nil {
		return fmt.Errorf("failed to write magic bytes: %w", err)
	}

	if err := binary.Write(w, binary.BigEndian, h.Version); err != nil {
		return fmt.Errorf("failed to write version: %w", err)
	}
	if err := binary.Write(w, binary.BigEndian, uint8(h.CipherSuite)); err != nil {
		return fmt.Errorf("failed to write cipher suite: %w", err)
	}
	if err := binary.Write(w, binary.BigEndian, h.KDFMode); err != nil {
		return fmt.Errorf("failed to write KDF mode: %w", err)
	}
	if _, err := w.Write(h.Salt[:]); err != nil {
		return fmt.Errorf("failed to write salt: %w", err)
	}

	// Write WrappedDEK with uint16 length prefix
	if len(h.WrappedDEK) > 65535 {
		return errors.New("wrapped DEK exceeds maximum uint16 size")
	}
	if err := binary.Write(w, binary.BigEndian, uint16(len(h.WrappedDEK))); err != nil {
		return fmt.Errorf("failed to write wrapped DEK length: %w", err)
	}
	if _, err := w.Write(h.WrappedDEK); err != nil {
		return fmt.Errorf("failed to write wrapped DEK: %w", err)
	}

	// Write MetadataJSON with uint32 length prefix
	if err := binary.Write(w, binary.BigEndian, uint32(len(h.MetadataJSON))); err != nil {
		return fmt.Errorf("failed to write metadata length: %w", err)
	}
	if len(h.MetadataJSON) > 0 {
		if _, err := w.Write(h.MetadataJSON); err != nil {
			return fmt.Errorf("failed to write metadata: %w", err)
		}
	}

	return nil
}

// ReadHeader parses and validates the EnvelopeHeader from the provided reader.
func ReadHeader(r io.Reader) (*EnvelopeHeader, error) {
	var magic [8]byte
	if _, err := io.ReadFull(r, magic[:]); err != nil {
		return nil, fmt.Errorf("failed to read magic header: %w", err)
	}
	if magic != MagicBytes {
		return nil, ErrInvalidMagic
	}

	h := &EnvelopeHeader{}
	if err := binary.Read(r, binary.BigEndian, &h.Version); err != nil {
		return nil, fmt.Errorf("failed to read version: %w", err)
	}
	if h.Version > CurrentEnvelopeVersion {
		return nil, fmt.Errorf("%w: header version %d > current %d", ErrUnsupportedVer, h.Version, CurrentEnvelopeVersion)
	}

	var suiteByte uint8
	if err := binary.Read(r, binary.BigEndian, &suiteByte); err != nil {
		return nil, fmt.Errorf("failed to read cipher suite: %w", err)
	}
	h.CipherSuite = CipherSuite(suiteByte)

	if err := binary.Read(r, binary.BigEndian, &h.KDFMode); err != nil {
		return nil, fmt.Errorf("failed to read KDF mode: %w", err)
	}

	if _, err := io.ReadFull(r, h.Salt[:]); err != nil {
		return nil, fmt.Errorf("failed to read salt: %w", err)
	}

	var dekLen uint16
	if err := binary.Read(r, binary.BigEndian, &dekLen); err != nil {
		return nil, fmt.Errorf("failed to read wrapped DEK length: %w", err)
	}
	h.WrappedDEK = make([]byte, dekLen)
	if _, err := io.ReadFull(r, h.WrappedDEK); err != nil {
		return nil, fmt.Errorf("failed to read wrapped DEK: %w", err)
	}

	var metaLen uint32
	if err := binary.Read(r, binary.BigEndian, &metaLen); err != nil {
		return nil, fmt.Errorf("failed to read metadata length: %w", err)
	}
	if metaLen > 0 {
		h.MetadataJSON = make([]byte, metaLen)
		if _, err := io.ReadFull(r, h.MetadataJSON); err != nil {
			return nil, fmt.Errorf("failed to read metadata payload: %w", err)
		}
	}

	return h, nil
}
