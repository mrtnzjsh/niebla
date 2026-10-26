package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io"

	"golang.org/x/crypto/chacha20poly1305"
)

// CipherSuite identifies the authenticated cipher algorithm.
type CipherSuite uint8

const (
	// SuiteAES256GCM is AES-256 in Galois/Counter Mode (NIST SP 800-38D).
	SuiteAES256GCM CipherSuite = 0x01
	// SuiteChaCha20Poly1305 is ChaCha20-Poly1305 AEAD (RFC 8439).
	SuiteChaCha20Poly1305 CipherSuite = 0x02

	// NonceSize is the standard 96-bit (12-byte) nonce size for GCM and ChaCha20-Poly1305.
	NonceSize = 12

	// DefaultStreamChunkSize is 64KB per streaming encrypted block.
	DefaultStreamChunkSize = 64 * 1024
)

var (
	ErrAuthFailed        = errors.New("crypto: message authentication failed (data corrupted or tampered)")
	ErrInvalidCiphertext = errors.New("crypto: invalid ciphertext length or structure")
	ErrTruncatedStream   = errors.New("crypto: stream was truncated or incomplete")
)

// AEADCipher defines authenticated encryption and decryption operations.
type AEADCipher interface {
	Suite() CipherSuite
	Encrypt(plaintext, additionalData []byte) (ciphertext []byte, nonce []byte, err error)
	Decrypt(ciphertext, nonce, additionalData []byte) (plaintext []byte, err error)
	StreamEncrypt(r io.Reader, w io.Writer, additionalData []byte) error
	StreamDecrypt(r io.Reader, w io.Writer, additionalData []byte) error
}

// -----------------------------------------------------------------------------
// AES-256-GCM Implementation
// -----------------------------------------------------------------------------

type aesGCMCipher struct {
	key []byte
}

// NewAES256GCM creates an AEADCipher backed by AES-256-GCM. Key must be exactly 32 bytes.
func NewAES256GCM(key []byte) (AEADCipher, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("aes-256 requires a 32-byte key, got %d bytes", len(key))
	}
	keyCopy := make([]byte, 32)
	copy(keyCopy, key)
	return &aesGCMCipher{key: keyCopy}, nil
}

func (c *aesGCMCipher) Suite() CipherSuite {
	return SuiteAES256GCM
}

func (c *aesGCMCipher) getAEAD() (cipher.AEAD, error) {
	block, err := aes.NewCipher(c.key)
	if err != nil {
		return nil, fmt.Errorf("failed to create AES cipher block: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("failed to create GCM AEAD: %w", err)
	}
	return gcm, nil
}

func (c *aesGCMCipher) Encrypt(plaintext, additionalData []byte) ([]byte, []byte, error) {
	gcm, err := c.getAEAD()
	if err != nil {
		return nil, nil, err
	}

	nonce := make([]byte, NonceSize)
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, nil, fmt.Errorf("failed to generate random nonce: %w", err)
	}

	ciphertext := gcm.Seal(nil, nonce, plaintext, additionalData)
	return ciphertext, nonce, nil
}

func (c *aesGCMCipher) Decrypt(ciphertext, nonce, additionalData []byte) ([]byte, error) {
	if len(nonce) != NonceSize {
		return nil, fmt.Errorf("invalid nonce size: expected %d, got %d", NonceSize, len(nonce))
	}

	gcm, err := c.getAEAD()
	if err != nil {
		return nil, err
	}

	plaintext, err := gcm.Open(nil, nonce, ciphertext, additionalData)
	if err != nil {
		return nil, ErrAuthFailed
	}
	return plaintext, nil
}

func (c *aesGCMCipher) StreamEncrypt(r io.Reader, w io.Writer, additionalData []byte) error {
	gcm, err := c.getAEAD()
	if err != nil {
		return err
	}
	return streamEncryptAEAD(gcm, r, w, additionalData)
}

func (c *aesGCMCipher) StreamDecrypt(r io.Reader, w io.Writer, additionalData []byte) error {
	gcm, err := c.getAEAD()
	if err != nil {
		return err
	}
	return streamDecryptAEAD(gcm, r, w, additionalData)
}

// -----------------------------------------------------------------------------
// ChaCha20-Poly1305 Implementation
// -----------------------------------------------------------------------------

type chachaCipher struct {
	key []byte
}

// NewChaCha20Poly1305 creates an AEADCipher backed by ChaCha20-Poly1305. Key must be exactly 32 bytes.
func NewChaCha20Poly1305(key []byte) (AEADCipher, error) {
	if len(key) != chacha20poly1305.KeySize {
		return nil, fmt.Errorf("chacha20-poly1305 requires a 32-byte key, got %d bytes", len(key))
	}
	keyCopy := make([]byte, 32)
	copy(keyCopy, key)
	return &chachaCipher{key: keyCopy}, nil
}

func (c *chachaCipher) Suite() CipherSuite {
	return SuiteChaCha20Poly1305
}

func (c *chachaCipher) getAEAD() (cipher.AEAD, error) {
	return chacha20poly1305.New(c.key)
}

func (c *chachaCipher) Encrypt(plaintext, additionalData []byte) ([]byte, []byte, error) {
	aead, err := c.getAEAD()
	if err != nil {
		return nil, nil, err
	}

	nonce := make([]byte, NonceSize)
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, nil, fmt.Errorf("failed to generate random nonce: %w", err)
	}

	ciphertext := aead.Seal(nil, nonce, plaintext, additionalData)
	return ciphertext, nonce, nil
}

func (c *chachaCipher) Decrypt(ciphertext, nonce, additionalData []byte) ([]byte, error) {
	if len(nonce) != NonceSize {
		return nil, fmt.Errorf("invalid nonce size: expected %d, got %d", NonceSize, len(nonce))
	}

	aead, err := c.getAEAD()
	if err != nil {
		return nil, err
	}

	plaintext, err := aead.Open(nil, nonce, ciphertext, additionalData)
	if err != nil {
		return nil, ErrAuthFailed
	}
	return plaintext, nil
}

func (c *chachaCipher) StreamEncrypt(r io.Reader, w io.Writer, additionalData []byte) error {
	aead, err := c.getAEAD()
	if err != nil {
		return err
	}
	return streamEncryptAEAD(aead, r, w, additionalData)
}

func (c *chachaCipher) StreamDecrypt(r io.Reader, w io.Writer, additionalData []byte) error {
	aead, err := c.getAEAD()
	if err != nil {
		return err
	}
	return streamDecryptAEAD(aead, r, w, additionalData)
}

// -----------------------------------------------------------------------------
// Streaming Chunk Framer with Anti-Truncation AEAD
// -----------------------------------------------------------------------------
// Chunk Format on wire:
// [4 bytes: uint32 payload_len]
// [1 byte: uint8 is_last_chunk (0x00 = more chunks, 0x01 = final chunk)]
// [12 bytes: chunk_nonce]
// [N bytes: ciphertext (payload_len - 12 bytes, containing auth tag)]

func streamEncryptAEAD(aead cipher.AEAD, r io.Reader, w io.Writer, additionalData []byte) error {
	buf := make([]byte, DefaultStreamChunkSize)
	var seq uint64 = 0

	for {
		n, err := io.ReadFull(r, buf)
		if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
			return fmt.Errorf("stream read error: %w", err)
		}

		isLast := err == io.EOF || err == io.ErrUnexpectedEOF || n < len(buf)

		// Generate unique chunk nonce: 8 bytes sequence number + 4 bytes random
		chunkNonce := make([]byte, NonceSize)
		binary.BigEndian.PutUint64(chunkNonce[0:8], seq)
		if _, randErr := io.ReadFull(rand.Reader, chunkNonce[8:12]); randErr != nil {
			return fmt.Errorf("failed to generate chunk nonce: %w", randErr)
		}

		// Additional authenticated data includes stream sequence and isLast flag
		chunkAAD := append([]byte(nil), additionalData...)
		chunkAAD = binary.BigEndian.AppendUint64(chunkAAD, seq)
		if isLast {
			chunkAAD = append(chunkAAD, 0x01)
		} else {
			chunkAAD = append(chunkAAD, 0x00)
		}

		ciphertext := aead.Seal(nil, chunkNonce, buf[:n], chunkAAD)
		chunkLen := uint32(len(chunkNonce) + len(ciphertext))

		// Write frame header
		if err := binary.Write(w, binary.BigEndian, chunkLen); err != nil {
			return fmt.Errorf("failed to write chunk length: %w", err)
		}
		var lastByte uint8 = 0
		if isLast {
			lastByte = 1
		}
		if err := binary.Write(w, binary.BigEndian, lastByte); err != nil {
			return fmt.Errorf("failed to write chunk flag: %w", err)
		}

		// Write nonce and ciphertext
		if _, err := w.Write(chunkNonce); err != nil {
			return fmt.Errorf("failed to write chunk nonce: %w", err)
		}
		if _, err := w.Write(ciphertext); err != nil {
			return fmt.Errorf("failed to write chunk ciphertext: %w", err)
		}

		if isLast {
			break
		}
		seq++
	}

	return nil
}

func streamDecryptAEAD(aead cipher.AEAD, r io.Reader, w io.Writer, additionalData []byte) error {
	var seq uint64 = 0
	foundLast := false

	for {
		var chunkLen uint32
		if err := binary.Read(r, binary.BigEndian, &chunkLen); err != nil {
			if errors.Is(err, io.EOF) {
				if foundLast {
					return nil
				}
				return ErrTruncatedStream
			}
			return fmt.Errorf("failed to read chunk length: %w", err)
		}

		var lastByte uint8
		if err := binary.Read(r, binary.BigEndian, &lastByte); err != nil {
			return ErrTruncatedStream
		}
		isLast := lastByte == 1

		if chunkLen < NonceSize {
			return ErrInvalidCiphertext
		}

		chunkNonce := make([]byte, NonceSize)
		if _, err := io.ReadFull(r, chunkNonce); err != nil {
			return ErrTruncatedStream
		}

		cipherLen := chunkLen - NonceSize
		ciphertext := make([]byte, cipherLen)
		if _, err := io.ReadFull(r, ciphertext); err != nil {
			return ErrTruncatedStream
		}

		chunkAAD := append([]byte(nil), additionalData...)
		chunkAAD = binary.BigEndian.AppendUint64(chunkAAD, seq)
		if isLast {
			chunkAAD = append(chunkAAD, 0x01)
		} else {
			chunkAAD = append(chunkAAD, 0x00)
		}

		plaintext, err := aead.Open(nil, chunkNonce, ciphertext, chunkAAD)
		if err != nil {
			return ErrAuthFailed
		}

		if _, err := w.Write(plaintext); err != nil {
			return fmt.Errorf("failed to write decrypted chunk: %w", err)
		}

		if isLast {
			foundLast = true
			break
		}
		seq++
	}

	return nil
}
