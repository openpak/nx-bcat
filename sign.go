package main

// The key side of News publishing, moved here from the news service (internal/newsadmin
// publish.go): news now builds an unsigned seed, and this service is the only thing that holds
// the OpenPak BCAT signing key and the News AES key.

import (
	"bytes"
	"crypto/rsa"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"nx-bcat/bcatcontainer"
)

// publicKeyPEM is the key the console patch installs (news tools/news-patch). Every container
// is verified against it, independently of the signing key.
//
//go:embed openpak-bcat-public.pem
var publicKeyPEM []byte

type keys struct {
	signing *rsa.PrivateKey
	verify  *rsa.PublicKey
	aes     []byte
	index   byte
}

// loadKeys reads both keys and checks the signing key belongs to the verification key. Errors
// never carry key material.
func loadKeys(signingPath, aesPath string) (*keys, error) {
	verify, err := bcatcontainer.ParsePublicKeyPEM(publicKeyPEM)
	if err != nil {
		return nil, err
	}
	pem, err := os.ReadFile(signingPath)
	if err != nil {
		return nil, errors.New("signing key unreadable")
	}
	signing, err := bcatcontainer.ParsePrivateKeyPEM(pem)
	if err != nil {
		return nil, fmt.Errorf("signing key: %w", err)
	}
	if !signing.PublicKey.Equal(verify) {
		return nil, errors.New("the signing key does not match the OpenPak console verification key")
	}
	aes, err := os.ReadFile(aesPath)
	if err != nil {
		return nil, errors.New("AES key unreadable")
	}
	if n := len(aes); n != 16 && n != 24 && n != 32 {
		return nil, errors.New("AES key must be 16, 24 or 32 raw bytes")
	}
	return &keys{signing: signing, verify: verify, aes: aes}, nil
}

// signTree is news tools/news-cdn/sign-seed.sh in-process: every *.msgpack and icon.jpg except
// manifest.json becomes a .bcat container, each verified independently against the console's
// public key (size = header + payload, exact plaintext, valid signature); live articles also get
// their plaintext local.msgpack; SHA256SUMS lists the tree.
func (k *keys) signTree(in, out string) (int, error) {
	if err := os.MkdirAll(out, 0755); err != nil {
		return 0, err
	}
	var files []string
	err := filepath.WalkDir(in, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type().IsRegular() && (strings.HasSuffix(d.Name(), ".msgpack") || d.Name() == "icon.jpg") {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	sort.Strings(files)
	count := 0
	for _, path := range files {
		rel, _ := filepath.Rel(in, path)
		rel = filepath.ToSlash(rel)
		plain, err := os.ReadFile(path)
		if err != nil {
			return count, err
		}
		dst := strings.TrimSuffix(strings.TrimSuffix(rel, ".msgpack"), ".jpg") + ".bcat"
		container, err := bcatcontainer.Encode(plain, k.aes, k.index, k.signing)
		if err != nil {
			return count, fmt.Errorf("%s: %w", rel, err)
		}
		if len(container) != bcatcontainer.HeaderSize+len(plain) {
			return count, fmt.Errorf("%s: size mismatch (lists advertise 288 + payload bytes)", rel)
		}
		got, err := bcatcontainer.Verify(container, k.aes, k.verify)
		if err != nil {
			return count, fmt.Errorf("%s: verification failed: %w", rel, err)
		}
		if !bytes.Equal(got, plain) {
			return count, fmt.Errorf("%s: verification failed: plaintext differs", rel)
		}
		if err := writeFileMkdir(filepath.Join(out, dst), container); err != nil {
			return count, err
		}
		if strings.HasSuffix(rel, "/article.msgpack") && !strings.HasPrefix(rel, "retired/") {
			local := strings.TrimSuffix(rel, "article.msgpack") + "local.msgpack"
			if err := writeFileMkdir(filepath.Join(out, local), plain); err != nil {
				return count, err
			}
		}
		// The catalog and each live channel's detail, icon and archives stay readable
		// (<name>.plain) for the local routes.
		if base := filepath.Base(rel); rel == "catalog.msgpack" ||
			((base == "detail.msgpack" || base == "icon.jpg" || base == "archives.msgpack" || base == "summary.msgpack") &&
				!strings.HasPrefix(rel, "retired/") && !strings.HasPrefix(rel, "titles/")) {
			if err := writeFileMkdir(filepath.Join(out, strings.TrimSuffix(rel, filepath.Ext(rel))+".plain"), plain); err != nil {
				return count, err
			}
		}
		count++
	}
	return count, writeSums(out)
}

func writeFileMkdir(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

// writeSums writes a sha256sum -c compatible SHA256SUMS (self-excluded).
func writeSums(dir string) error {
	var lines []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || !d.Type().IsRegular() || d.Name() == "SHA256SUMS" {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, path)
		sum := sha256.Sum256(data)
		lines = append(lines, hex.EncodeToString(sum[:])+"  ./"+filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return err
	}
	sort.Slice(lines, func(i, j int) bool { return lines[i][66:] < lines[j][66:] })
	return os.WriteFile(filepath.Join(dir, "SHA256SUMS"), []byte(strings.Join(lines, "\n")+"\n"), 0644)
}
