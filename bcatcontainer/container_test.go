// SPDX-License-Identifier: AGPL-3.0-only
package bcatcontainer

import (
	"bytes"
	"crypto"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"testing"
)

func TestEncode(t *testing.T) {
	signing, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	for _, size := range []int{16, 24, 32} {
		key := bytes.Repeat([]byte{0x42}, size)
		// Unaligned length exercises the partial final CTR block.
		plain := append([]byte{0x91, 0x81, 0xa4}, bytes.Repeat([]byte("OpenPak"), 19)...)
		encoded, err := Encode(plain, key, 0, signing)
		if err != nil {
			t.Fatal(err)
		}
		if len(encoded) != HeaderSize+len(plain) || string(encoded[:4]) != "bcat" || encoded[4] != 1 || encoded[5] != byte((size-16)/8+1) || encoded[6] != 1 || encoded[7] != 0 || !bytes.Equal(encoded[8:16], make([]byte, 8)) {
			t.Fatal("bad framing")
		}
		block, _ := aes.NewCipher(key)
		decoded := make([]byte, len(plain))
		cipher.NewCTR(block, encoded[16:32]).XORKeyStream(decoded, encoded[HeaderSize:])
		if !bytes.Equal(decoded, plain) {
			t.Fatal("plaintext differs")
		}
		hash := sha256.Sum256(decoded)
		if err := rsa.VerifyPKCS1v15(&signing.PublicKey, crypto.SHA256, hash[:], encoded[32:HeaderSize]); err != nil {
			t.Fatal(err)
		}
		encryptedHash := sha256.Sum256(encoded[HeaderSize:])
		if rsa.VerifyPKCS1v15(&signing.PublicKey, crypto.SHA256, encryptedHash[:], encoded[32:HeaderSize]) == nil {
			t.Fatal("signed ciphertext instead of plaintext")
		}
		encoded[HeaderSize] ^= 1
		cipher.NewCTR(block, encoded[16:32]).XORKeyStream(decoded, encoded[HeaderSize:])
		hash = sha256.Sum256(decoded)
		if rsa.VerifyPKCS1v15(&signing.PublicKey, crypto.SHA256, hash[:], encoded[32:HeaderSize]) == nil {
			t.Fatal("accepted corruption")
		}
		again, err := Encode(plain, key, 0, signing)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Equal(encoded[16:32], again[16:32]) {
			t.Fatal("IV reused")
		}
	}
	for _, tc := range []struct {
		plain, key []byte
		index      byte
		signing    *rsa.PrivateKey
	}{
		{nil, make([]byte, 32), 0, signing}, {make([]byte, MaxPayload+1), make([]byte, 32), 0, signing},
		{[]byte{1}, make([]byte, 17), 0, signing}, {[]byte{1}, make([]byte, 32), 16, signing}, {[]byte{1}, make([]byte, 32), 0, nil},
	} {
		if _, err := Encode(tc.plain, tc.key, tc.index, tc.signing); err == nil {
			t.Fatal("invalid input accepted")
		}
	}
}

func TestSignedPlaintext(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	plain := []byte("native BCAT payload")
	container, err := Encode(plain, nil, 0, key)
	if err != nil {
		t.Fatal(err)
	}
	if container[5] != 0 || !bytes.Equal(container[HeaderSize:], plain) {
		t.Fatal("invalid plaintext crypto framing")
	}
	decoded, err := Verify(container, nil, &key.PublicKey)
	if err != nil || !bytes.Equal(decoded, plain) {
		t.Fatalf("verification: %v", err)
	}
	container[HeaderSize] ^= 1
	if _, err := Verify(container, nil, &key.PublicKey); err == nil {
		t.Fatal("accepted corrupted plaintext")
	}
}
