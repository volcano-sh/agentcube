/*
Copyright The Volcano Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package store

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
)

var ErrDecryptionFailed = errors.New("failed to decrypt data")

// Crypto handles symmetric encryption and decryption for sensitive data (e.g. session private keys).
type Crypto struct {
	aead cipher.AEAD
}

// NewCrypto creates a new Crypto instance with the given AES key.
// The key must be 16, 24, or 32 bytes to select AES-128, AES-192, or AES-256.
func NewCrypto(key []byte) (*Crypto, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("failed to create AES cipher: %w", err)
	}

	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("failed to create GCM cipher: %w", err)
	}

	return &Crypto{aead: aead}, nil
}

// Encrypt encrypts the plaintext using AES-GCM and prepends the nonce.
func (c *Crypto) Encrypt(plaintext []byte) ([]byte, error) {
	if c == nil {
		return plaintext, nil
	}

	nonce := make([]byte, c.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("failed to generate nonce: %w", err)
	}

	return c.aead.Seal(nonce, nonce, plaintext, nil), nil
}

// Decrypt decrypts the ciphertext that was encrypted by Encrypt().
func (c *Crypto) Decrypt(ciphertext []byte) ([]byte, error) {
	if c == nil {
		return ciphertext, nil
	}

	if len(ciphertext) < c.aead.NonceSize() {
		return nil, ErrDecryptionFailed
	}

	nonce, ciphertextData := ciphertext[:c.aead.NonceSize()], ciphertext[c.aead.NonceSize():]
	plaintext, err := c.aead.Open(nil, nonce, ciphertextData, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrDecryptionFailed, err)
	}

	return plaintext, nil
}
