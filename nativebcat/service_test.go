package nativebcat

import (
	"bytes"
	"context"
	"crypto/md5"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"nx-bcat/bcatcontainer"
	"nx-bcat/newsformat"
)

type testSource struct {
	d       *Dataset
	data    []byte
	expired bool
}

func (s *testSource) CurrentDataset(_ context.Context, title string) (*Dataset, time.Time, error) {
	if s.expired || title != s.d.TitleID {
		return nil, time.Time{}, ErrNotFound
	}
	return s.d, time.Unix(1700000000, 0), nil
}

func (s *testSource) OpenObject(context.Context, string) (io.ReadSeekCloser, int64, time.Time, error) {
	return testObject{bytes.NewReader(s.data)}, int64(len(s.data)), time.Time{}, nil
}

type testObject struct{ *bytes.Reader }

func (testObject) Close() error { return nil }

func TestNativeAndEmulatorSharePayloadBytes(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	data := []byte("shared game schedule")
	hash := sha256.Sum256(data)
	title := "0100f8f0000a2000"
	source := &testSource{data: data, d: &Dataset{TitleID: title,
		Files: []DatasetFile{{Path: "vsdata/VSSetting_0.byaml", Key: "key", Size: int64(len(data)), SHA256: hex.EncodeToString(hash[:])}}}}
	s, err := NewService(source, key, "https://bcat-data-lp1.cdn.nintendo.net")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Validate(context.Background(), []string{title}); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	s.RegisterRoutes(mux)
	list := httptest.NewRecorder()
	mux.ServeHTTP(list, httptest.NewRequest("GET", "/api/nx/v1/list/nx_data_"+title, nil))
	decoded, err := bcatcontainer.Verify(list.Body.Bytes(), nil, &key.PublicKey)
	if err != nil || list.Code != 200 {
		t.Fatalf("list: %d %v", list.Code, err)
	}
	digest := md5.Sum(data)
	path := "/bcat/" + title + "/" + hex.EncodeToString(hash[:8]) + "/vsdata/VSSetting_0.byaml"
	// The signed list must reference the shared file with native metadata.
	for _, value := range []string{"required_application_version", "directories", "vsdata", "sync", "VSSetting_0.byaml", "https://bcat-data-lp1.cdn.nintendo.net" + path, digestText(digest)} {
		if !bytes.Contains(decoded, []byte(value)) {
			t.Errorf("list lacks %q", value)
		}
	}
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
	payload, err := bcatcontainer.Verify(w.Body.Bytes(), nil, &key.PublicKey)
	if err != nil || !bytes.Equal(payload, data) {
		t.Fatalf("native payload differs from shared bytes: %v", err)
	}
	if w.Body.Len() != bcatcontainer.HeaderSize+len(data) {
		t.Fatal("wrong container size")
	}
	r := httptest.NewRequest("GET", path, nil)
	r.Header.Set("If-None-Match", w.Header().Get("ETag"))
	conditional := httptest.NewRecorder()
	mux.ServeHTTP(conditional, r)
	if conditional.Code != 304 || conditional.Body.Len() != 0 {
		t.Fatalf("conditional GET: %d", conditional.Code)
	}
	r = httptest.NewRequest("GET", path, nil)
	r.Header.Set("Range", "bytes=0-3")
	partial := httptest.NewRecorder()
	mux.ServeHTTP(partial, r)
	if partial.Code != 206 || partial.Body.String() != "bcat" {
		t.Fatalf("Range response: %d %q", partial.Code, partial.Body.String())
	}
	// Expiry also gates signed files already compiled into the native cache.
	source.expired = true
	expired := httptest.NewRecorder()
	mux.ServeHTTP(expired, httptest.NewRequest("GET", path, nil))
	if expired.Code != 404 {
		t.Fatalf("expired file: %d", expired.Code)
	}
	empty := httptest.NewRecorder()
	mux.ServeHTTP(empty, httptest.NewRequest("GET", "/api/nx/v1/list/nx_data_"+title, nil))
	payload, err = bcatcontainer.Verify(empty.Body.Bytes(), nil, &key.PublicKey)
	expected, _ := newsformat.Encode(newsformat.Map{{Key: "required_application_version", Value: uint64(0)}, {Key: "directories", Value: []any{}}})
	if empty.Code != 200 || err != nil || !bytes.Equal(payload, expected) {
		t.Fatalf("expired list: %d %v", empty.Code, err)
	}
}

func TestNativeDigestRoundTripMatchesFirmwareWordParser(t *testing.T) {
	digest := [16]byte{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15}
	if got := digestText(digest); got != "07060504030201000f0e0d0c0b0a0908" {
		t.Fatal(got)
	}
}
