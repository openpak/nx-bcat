// SPDX-License-Identifier: AGPL-3.0-only

// Package nativebcat renders the shared game datasets for the native BCAT
// daemon. Wire-format sources and remaining hardware gates are in
// docs/native-game-bcat.md. This renderer does not own game content.
package nativebcat

import (
	"bytes"
	"context"
	"crypto/md5"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"nx-bcat/bcatcontainer"
	"nx-bcat/newsformat"
)

type Source interface {
	CurrentDataset(context.Context, string) (*Dataset, time.Time, error)
	OpenObject(context.Context, string) (io.ReadSeekCloser, int64, time.Time, error)
}

type snapshot struct {
	list  []byte
	files map[string][]byte
	mod   time.Time
}

type Service struct {
	source Source
	key    *rsa.PrivateKey
	origin string
	empty  map[string][]byte // signed empty list per title (topic_id carries the title)
	mutex  sync.Mutex
	cache  map[string]*snapshot
}

func NewService(src Source, key *rsa.PrivateKey, origin string) (*Service, error) {
	u, err := url.Parse(origin)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil ||
		u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("native data origin must be a bare HTTPS origin")
	}
	s := &Service{source: src, key: key, origin: origin, cache: map[string]*snapshot{}, empty: map[string][]byte{}}
	if _, err := s.emptyList("0000000000000000"); err != nil {
		return nil, err
	}
	return s, nil
}

// emptyList is the signed list a title with nothing published gets.
func (s *Service) emptyList(title string) ([]byte, error) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	if b, ok := s.empty[title]; ok {
		return b, nil
	}
	b, err := s.signList(title, []any{})
	if err != nil {
		return nil, err
	}
	s.empty[title] = b
	return b, nil
}

// signList encodes the data list. The firmware's list parser refuses the document at the start of
// "directories" unless topic_id, service_status, na_required and required_application_version have
// all been read before it (23.0.1 bcat, the "$.directories[]" callback), so they come first and
// "directories" last. Without them every list, even an empty one, fails the sync with 2122-0203.
func (s *Service) signList(title string, directories []any) ([]byte, error) {
	plain, err := newsformat.Encode(newsformat.Map{
		{Key: "topic_id", Value: "nx_data_" + title},
		{Key: "service_status", Value: "in_service"},
		{Key: "na_required", Value: false},
		{Key: "test_distribution", Value: false},
		{Key: "required_application_version", Value: uint64(0)},
		{Key: "directories", Value: directories},
	})
	if err != nil {
		return nil, err
	}
	// Crypto type 0 is the documented signed plaintext format. It needs
	// the OpenPak verification-key patch, but no per-title encryption secret.
	return bcatcontainer.Encode(plain, nil, 0, s.key)
}

func (s *Service) compile(ctx context.Context, title string) (*snapshot, error) {
	// Check validity on every request, including requests for cached files.
	d, mod, err := s.source.CurrentDataset(ctx, title)
	if err != nil {
		return nil, err
	}
	s.mutex.Lock()
	defer s.mutex.Unlock()
	if cached := s.cache[title]; cached != nil && cached.mod.Equal(mod) {
		return cached, nil
	}
	result := &snapshot{files: map[string][]byte{}, mod: mod}
	groups := map[string][]any{}
	files := append([]DatasetFile(nil), d.Files...)
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	for _, f := range files {
		directory, filename, ok := strings.Cut(f.Path, "/")
		if !ok || ValidateFilePath(f.Path) != nil || strings.Contains(filename, "/") {
			return nil, fmt.Errorf("invalid native BCAT path %q", f.Path)
		}
		rc, _, _, err := s.source.OpenObject(ctx, f.Key)
		if err != nil {
			return nil, err
		}
		plain, err := io.ReadAll(io.LimitReader(rc, bcatcontainer.MaxPayload+1))
		rc.Close()
		if err != nil {
			return nil, err
		}
		hash := sha256.Sum256(plain)
		if int64(len(plain)) != f.Size || hex.EncodeToString(hash[:]) != f.SHA256 {
			return nil, errors.New("native BCAT source does not match the shared manifest")
		}
		body, err := bcatcontainer.Encode(plain, nil, 0, s.key)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", f.Path, err)
		}
		path := "/bcat/" + title + "/" + f.SHA256[:16] + "/" + f.Path
		dataURL := s.origin + path
		if len(dataURL) > 159 {
			return nil, errors.New("native BCAT data URL exceeds the firmware buffer")
		}
		result.files[path] = body
		digest := md5.Sum(plain)
		groups[directory] = append(groups[directory], newsformat.Map{
			{Key: "filename", Value: filename},
			{Key: "url", Value: dataURL},
			{Key: "size", Value: uint64(len(body))},
			{Key: "digest", Value: digestText(digest)},
			{Key: "data_id", Value: binary.BigEndian.Uint64(hash[:8])},
		})
	}
	names := make([]string, 0, len(groups))
	for name := range groups {
		names = append(names, name)
	}
	sort.Strings(names)
	directories := []any{}
	for _, name := range names {
		encoded, err := newsformat.Encode(groups[name])
		if err != nil {
			return nil, err
		}
		// The directory digest is an opaque revision marker. Derive it
		// from the complete, ordered descriptors so any file change updates it.
		digest := md5.Sum(encoded)
		directories = append(directories, newsformat.Map{
			{Key: "name", Value: name},
			{Key: "mode", Value: "sync"},
			{Key: "digest", Value: digestText(digest)},
			{Key: "data_list", Value: groups[name]},
		})
	}
	result.list, err = s.signList(title, directories)
	if err != nil {
		return nil, err
	}
	s.cache[title] = result
	return result, nil
}

// Validate prebuilds every currently published title before the listener starts.
func (s *Service) Validate(ctx context.Context, titles []string) error {
	for _, title := range titles {
		if _, err := s.compile(ctx, title); err != nil {
			return fmt.Errorf("native BCAT title %s: %w", title, err)
		}
	}
	return nil
}

func (s *Service) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/nx/v1/list/{key}", func(w http.ResponseWriter, r *http.Request) {
		title, ok := strings.CutPrefix(r.PathValue("key"), "nx_data_")
		if !ok || ValidateSwitchTitleID(title) != nil {
			http.NotFound(w, r)
			return
		}
		snapshot, err := s.compile(r.Context(), title)
		if errors.Is(err, ErrNotFound) {
			empty, err := s.emptyList(title)
			if err != nil {
				http.Error(w, "native BCAT data unavailable", http.StatusInternalServerError)
				return
			}
			serve(w, r, empty, time.Time{})
		} else if err != nil {
			http.Error(w, "native BCAT data unavailable", http.StatusInternalServerError)
		} else {
			serve(w, r, snapshot.list, snapshot.mod)
		}
	})
	mux.HandleFunc("GET /bcat/{title}/{revision}/{directory}/{file}", func(w http.ResponseWriter, r *http.Request) {
		title := r.PathValue("title")
		if ValidateSwitchTitleID(title) != nil {
			http.NotFound(w, r)
			return
		}
		snapshot, err := s.compile(r.Context(), title)
		if errors.Is(err, ErrNotFound) {
			http.NotFound(w, r)
			return
		} else if err != nil {
			http.Error(w, "native BCAT data unavailable", http.StatusInternalServerError)
			return
		}
		body, ok := snapshot.files[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		serve(w, r, body, snapshot.mod)
	})
}

func serve(w http.ResponseWriter, r *http.Request, body []byte, mod time.Time) {
	hash := sha256.Sum256(body)
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("ETag", `"`+hex.EncodeToString(hash[:])+`"`)
	w.Header().Set("Cache-Control", "public, max-age=300")
	http.ServeContent(w, r, "", mod, bytes.NewReader(body))
}

// 23.0.1 parses a digest as two hexadecimal u64 values, storing each
// in native little-endian order (FUN_085a18f0), not as 16 hex byte pairs.
func digestText(digest [16]byte) string {
	return fmt.Sprintf("%016x%016x", binary.LittleEndian.Uint64(digest[:8]), binary.LittleEndian.Uint64(digest[8:]))
}
