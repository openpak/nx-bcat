// SPDX-License-Identifier: AGPL-3.0-only
// Package bcatcontainer encodes experimental containers for the OpenPak-key
// patched BCAT module. It contains no platform encryption secrets.
package bcatcontainer

import (
	"bytes"
	"crypto"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"io"
)

const HeaderSize = 0x120
const MaxPayload = 4 << 20

// Encode signs the plaintext with RSA PKCS#1 v1.5/SHA-256, then optionally encrypts it
// with AES-CTR. aesKey must already have been derived for the target title
// and secret index. A nil aesKey selects the documented plaintext crypto type 0.
// The IV starts at payload offset zero, excluding the header.
func Encode(plaintext, aesKey []byte, secretIndex byte, signingKey *rsa.PrivateKey) ([]byte, error) {
	if len(plaintext) == 0 || len(plaintext) > MaxPayload {
		return nil, errors.New("payload size outside supported range")
	}
	if secretIndex > 15 {
		return nil, errors.New("secret index must be 0..15")
	}
	if signingKey == nil || signingKey.N == nil || signingKey.N.BitLen() != 2048 || signingKey.E != 65537 {
		return nil, errors.New("need RSA-2048/65537 signing key")
	}
	if err := signingKey.Validate(); err != nil {
		return nil, errors.New("invalid RSA signing key")
	}
	var block cipher.Block
	var cryptoType byte
	if aesKey != nil {
		var err error
		block, err = aes.NewCipher(aesKey)
		if err != nil {
			return nil, err
		}
		cryptoType = byte((len(aesKey)-16)/8 + 1)
	}
	out := make([]byte, HeaderSize+len(plaintext))
	copy(out, "bcat")
	out[4], out[5], out[6], out[7] = 1, cryptoType, 1, secretIndex
	if block != nil {
		if _, err := io.ReadFull(rand.Reader, out[0x10:0x20]); err != nil {
			return nil, err
		}
	}
	digest := sha256.Sum256(plaintext)
	signature, err := rsa.SignPKCS1v15(rand.Reader, signingKey, crypto.SHA256, digest[:])
	if err != nil {
		return nil, err
	}
	copy(out[0x20:HeaderSize], signature)
	if block == nil {
		copy(out[HeaderSize:], plaintext)
	} else {
		cipher.NewCTR(block, out[0x10:0x20]).XORKeyStream(out[HeaderSize:], plaintext)
	}
	return out, nil
}

// Verify is the independent check tools/news-cdn/sign-seed.sh runs with
// openssl, done in-process: the container is exactly HeaderSize + payload
// bytes, carries a valid header, decrypts (AES-CTR, IV at 0x10) and its
// RSA PKCS#1 v1.5 / SHA-256 signature over the plaintext verifies against
// the public key. It returns the decrypted payload.
func Verify(container, aesKey []byte, pub *rsa.PublicKey) ([]byte, error) {
	if len(container) <= HeaderSize || len(container) > HeaderSize+MaxPayload {
		return nil, errors.New("container size outside supported range")
	}
	var cryptoType byte
	if aesKey != nil {
		cryptoType = byte((len(aesKey)-16)/8 + 1)
	}
	if string(container[:4]) != "bcat" || container[4] != 1 || container[6] != 1 || container[7] > 15 ||
		container[5] != cryptoType {
		return nil, errors.New("invalid container header")
	}
	plain := make([]byte, len(container)-HeaderSize)
	if aesKey == nil {
		copy(plain, container[HeaderSize:])
	} else {
		block, err := aes.NewCipher(aesKey)
		if err != nil {
			return nil, err
		}
		cipher.NewCTR(block, container[0x10:0x20]).XORKeyStream(plain, container[HeaderSize:])
	}
	digest := sha256.Sum256(plain)
	if err := rsa.VerifyPKCS1v15(pub, crypto.SHA256, digest[:], container[0x20:HeaderSize]); err != nil {
		return nil, errors.New("signature does not verify")
	}
	return plain, nil
}

// ParsePrivateKeyPEM reads one PKCS#1 or PKCS#8 RSA private key PEM block.
// Errors never include key material.
func ParsePrivateKeyPEM(encoded []byte) (*rsa.PrivateKey, error) {
	block, rest := pem.Decode(encoded)
	if block == nil || len(bytes.TrimSpace(rest)) != 0 {
		return nil, errors.New("expected one private-key PEM block")
	}
	switch block.Type {
	case "RSA PRIVATE KEY":
		k, err := x509.ParsePKCS1PrivateKey(block.Bytes)
		if err != nil {
			return nil, errors.New("invalid RSA private key")
		}
		return k, nil
	case "PRIVATE KEY":
		parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
		k, ok := parsed.(*rsa.PrivateKey)
		if err != nil || !ok {
			return nil, errors.New("invalid RSA private key")
		}
		return k, nil
	}
	return nil, errors.New("unsupported private-key PEM type")
}

// ParsePublicKeyPEM reads one PKIX or PKCS#1 RSA public key PEM block.
func ParsePublicKeyPEM(encoded []byte) (*rsa.PublicKey, error) {
	block, _ := pem.Decode(encoded)
	if block == nil {
		return nil, errors.New("expected a public-key PEM block")
	}
	switch block.Type {
	case "PUBLIC KEY":
		parsed, err := x509.ParsePKIXPublicKey(block.Bytes)
		k, ok := parsed.(*rsa.PublicKey)
		if err != nil || !ok {
			return nil, errors.New("invalid RSA public key")
		}
		return k, nil
	case "RSA PUBLIC KEY":
		return x509.ParsePKCS1PublicKey(block.Bytes)
	}
	return nil, errors.New("unsupported public-key PEM type")
}
