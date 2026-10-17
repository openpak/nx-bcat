// SPDX-License-Identifier: AGPL-3.0-only
package newsexperiment

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"nx-bcat/bcatcontainer"
)

func fixture(t *testing.T, dir, name string) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := bcatcontainer.Encode([]byte{0x90}, make([]byte, 32), 0, key)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), payload, 0600); err != nil {
		t.Fatal(err)
	}
}

func writeTree(t *testing.T, topics ...string) string {
	t.Helper()
	dir := t.TempDir()
	fixture(t, dir, "catalog.bcat")
	for _, topic := range topics {
		sub := filepath.Join(dir, topic)
		if err := os.Mkdir(sub, 0755); err != nil {
			t.Fatal(err)
		}
		for _, f := range []string{"detail.bcat", "icon.bcat", "archives.bcat"} {
			fixture(t, sub, f)
		}
	}
	return dir
}

func TestMultiChannelRoutes(t *testing.T) {
	dir := writeTree(t, "openpak_news", "openpak_games")
	h, err := Handler(dir, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	routes := []string{
		"/api/nx/v1/topics/catalog?l=en-GB&c%5B%5D=GB&c%5B%5D=GB",
		"/api/nx/v1/topics/openpak_news/detail",
		"/api/nx/v1/topics/openpak_news/icon",
		"/api/nx/v2/topics/openpak_news/online_archives?l=en-GB",
		"/api/nx/v1/topics/openpak_news/archives",
		"/api/nx/v1/topics/openpak_games/detail",
		"/api/nx/v1/topics/openpak_games/icon",
		"/api/nx/v2/topics/openpak_games/online_archives",
	}
	for _, path := range routes {
		r := httptest.NewRecorder()
		h.ServeHTTP(r, httptest.NewRequest("GET", path, nil))
		if r.Code != 200 || r.Header().Get("Content-Type") != "application/octet-stream" || r.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("bad response for %s: code=%d", path, r.Code)
		}
		if len(r.Body.Bytes()) <= 0x120 || string(r.Body.Bytes()[:4]) != "bcat" {
			t.Fatalf("not a container for %s", path)
		}
	}
	// Unknown topics and paths fail explicitly (no fallback configured).
	for _, path := range []string{
		"/api/nx/v1/topics/nx_news/detail",
		"/api/nx/v2/topics/openpak_help/online_archives",
		"/api/nx/v1/topics/openpak_games/icon/extra",
		"/catalog",
		"/api/nx/v1/topics/catalog/",
	} {
		r := httptest.NewRecorder()
		h.ServeHTTP(r, httptest.NewRequest("GET", path, nil))
		if r.Code != 404 {
			t.Fatalf("unknown route %s accepted: %d", path, r.Code)
		}
	}
	// Only the explicit summary/article resources are exposed.
	art := filepath.Join(dir, "openpak_news", "2609100001")
	if err := os.MkdirAll(art, 0755); err != nil {
		t.Fatal(err)
	}
	fixture(t, art, "article.bcat")
	fixture(t, art, "summary.bcat")
	h, err = Handler(dir, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRecorder()
	h.ServeHTTP(r, httptest.NewRequest("GET", "/api/nx/v1/topics/openpak_news/2609100001/article", nil))
	// The native module's downloader asked for exactly this path on
	// hardware (2026-09-16), so it serves the article container.
	want, err := os.ReadFile(filepath.Join(art, "article.bcat"))
	if err != nil {
		t.Fatal(err)
	}
	if r.Code != 200 || !bytes.Equal(r.Body.Bytes(), want) {
		t.Fatal("v1 article route mismatch")
	}
	for _, resource := range []string{"summary", "article"} {
		r = httptest.NewRecorder()
		h.ServeHTTP(r, httptest.NewRequest("GET", "/openpak/v1/topics/openpak_news/news/2609100001/"+resource, nil))
		want, _ := os.ReadFile(filepath.Join(art, resource+".bcat"))
		if r.Code != 200 || !bytes.Equal(r.Body.Bytes(), want) {
			t.Fatal("resource route mismatch", resource)
		}
	}
	for _, resource := range []string{"article-expired", "card", "manifest.json"} {
		r = httptest.NewRecorder()
		h.ServeHTTP(r, httptest.NewRequest("GET", "/openpak/v1/topics/openpak_news/news/2609100001/"+resource, nil))
		if r.Code != 404 {
			t.Fatal("unapproved resource exposed", resource)
		}
	}
	if err := os.Remove(filepath.Join(art, "summary.bcat")); err != nil {
		t.Fatal(err)
	}
	if _, err := Handler(dir, nil, nil); err == nil {
		t.Fatal("incomplete article accepted")
	}
}

func TestFallbackForUnknownTopic(t *testing.T) {
	dir := writeTree(t, "openpak_news")
	h, err := Handler(dir, nil, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(418) }))
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRecorder()
	h.ServeHTTP(r, httptest.NewRequest("GET", "/api/nx/v1/topics/nx_notice/detail", nil))
	if r.Code != 418 {
		t.Fatal("Nintendo-owned channel detail did not fall back")
	}
	r = httptest.NewRecorder()
	h.ServeHTTP(r, httptest.NewRequest("GET", "/other", nil))
	if r.Code != 418 {
		t.Fatal("fallback not used")
	}
}

func TestHandlerRejectsBadLayouts(t *testing.T) {
	t.Run("missing catalog", func(t *testing.T) {
		dir := t.TempDir()
		if _, err := Handler(dir, nil, nil); err == nil {
			t.Fatal("missing catalog accepted")
		}
	})
	t.Run("legacy single-channel layout", func(t *testing.T) {
		dir := writeTree(t, "openpak_news")
		fixture(t, dir, "detail.bcat")
		if _, err := Handler(dir, nil, nil); err == nil {
			t.Fatal("legacy flat fixture accepted")
		}
	})
	t.Run("incomplete channel", func(t *testing.T) {
		dir := writeTree(t, "openpak_news")
		if err := os.Remove(filepath.Join(dir, "openpak_news", "icon.bcat")); err != nil {
			t.Fatal(err)
		}
		if _, err := Handler(dir, nil, nil); err == nil {
			t.Fatal("channel without icon accepted")
		}
	})
	t.Run("invalid topic directory", func(t *testing.T) {
		dir := writeTree(t)
		if err := os.Mkdir(filepath.Join(dir, "..evil"), 0755); err != nil {
			t.Fatal(err)
		}
		if _, err := Handler(dir, nil, nil); err == nil {
			t.Fatal("invalid topic directory accepted")
		}
	})
	t.Run("empty lineup serves the catalog", func(t *testing.T) {
		dir := writeTree(t)
		h, err := Handler(dir, nil, nil)
		if err != nil {
			t.Fatalf("catalog-only tree rejected: %v", err)
		}
		r := httptest.NewRecorder()
		h.ServeHTTP(r, httptest.NewRequest("GET", "/api/nx/v1/topics/catalog", nil))
		if r.Code != 200 || string(r.Body.Bytes()[:4]) != "bcat" {
			t.Fatalf("catalog not served: code=%d", r.Code)
		}
		r = httptest.NewRecorder()
		h.ServeHTTP(r, httptest.NewRequest("GET", "/api/nx/v1/topics/openpak_news/detail", nil))
		if r.Code != 404 {
			t.Fatal("topic route served without channel fixtures")
		}
	})
	t.Run("json fixture rejected", func(t *testing.T) {
		dir := writeTree(t, "openpak_news")
		if err := os.WriteFile(filepath.Join(dir, "openpak_news", "detail.bcat"), []byte(`{"topics":[]}`), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := Handler(dir, nil, nil); err == nil {
			t.Fatal("JSON fixture accepted")
		}
	})
	t.Run("retired article without its container", func(t *testing.T) {
		dir := writeTree(t, "openpak_news")
		ret := filepath.Join(dir, "retired", "openpak_news", "2609100001")
		if err := os.MkdirAll(ret, 0755); err != nil {
			t.Fatal(err)
		}
		if _, err := Handler(dir, nil, nil); err == nil {
			t.Fatal("retired article directory without article.bcat accepted")
		}
	})
	t.Run("titles without none", func(t *testing.T) {
		dir := writeTree(t, "openpak_news")
		if err := os.Mkdir(filepath.Join(dir, "titles"), 0755); err != nil {
			t.Fatal(err)
		}
		fixture(t, filepath.Join(dir, "titles"), "0100abcd12340000.bcat")
		if _, err := Handler(dir, nil, nil); err == nil {
			t.Fatal("titles tree without none.bcat accepted")
		}
	})
}

func TestServedBytesAreStable(t *testing.T) {
	dir := writeTree(t, "openpak_news")
	h, err := Handler(dir, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	var first []byte
	for i := 0; i < 2; i++ {
		r := httptest.NewRecorder()
		h.ServeHTTP(r, httptest.NewRequest("GET", "/api/nx/v1/topics/openpak_news/detail", nil))
		if i == 0 {
			first = append([]byte(nil), r.Body.Bytes()...)
			continue
		}
		if !bytes.Equal(first, r.Body.Bytes()) {
			t.Fatal("fixture bytes changed between requests")
		}
	}
}

// The sync helper's plaintext route: index lines and byte-exact payloads,
// with the index sorted so two builds of the same tree agree.
func TestLocalSyncRoutes(t *testing.T) {
	dir := writeTree(t, "openpak_news", "openpak_games")
	payloads := map[string][]byte{
		"openpak_news/2609100002":  []byte("second news payload"),
		"openpak_news/2609100001":  []byte("first news payload"),
		"openpak_games/2609110101": []byte("games payload"),
	}
	for rel, payload := range payloads {
		art := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(art, 0755); err != nil {
			t.Fatal(err)
		}
		fixture(t, art, "article.bcat")
		fixture(t, art, "summary.bcat")
		if err := os.WriteFile(filepath.Join(art, "local.msgpack"), payload, 0600); err != nil {
			t.Fatal(err)
		}
	}
	// Decoded copies the signer leaves beside the containers: the catalog
	// and one channel's detail. openpak_games has none and gets no route.
	plain := map[string][]byte{"catalog.plain": []byte("catalog payload"), "openpak_news/detail.plain": []byte("detail payload"),
		"openpak_news/2609100001/summary.plain": []byte("summary payload")}
	for rel, payload := range plain {
		if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(rel)), payload, 0600); err != nil {
			t.Fatal(err)
		}
	}
	h, err := Handler(dir, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]string{
		"/openpak/v1/local/catalog":                              "catalog payload",
		"/openpak/v1/local/openpak_news/detail":                  "detail payload",
		"/openpak/v1/local/openpak_news/news/2609100001/summary": "summary payload",
	} {
		r := httptest.NewRecorder()
		h.ServeHTTP(r, httptest.NewRequest("GET", path, nil))
		if r.Code != 200 || r.Body.String() != want {
			t.Fatalf("%s: %d %q", path, r.Code, r.Body.String())
		}
	}
	r := httptest.NewRecorder()
	h.ServeHTTP(r, httptest.NewRequest("GET", "/openpak/v1/local/openpak_games/detail", nil))
	if r.Code == 200 {
		t.Fatal("a channel without detail.plain must not serve one")
	}
	r = httptest.NewRecorder()
	h.ServeHTTP(r, httptest.NewRequest("GET", "/openpak/v1/local/index", nil))
	want := "openpak_games 2609110101 13\nopenpak_news 2609100001 18\nopenpak_news 2609100002 19\n"
	if r.Code != 200 || r.Body.String() != want {
		t.Fatalf("index mismatch: %q", r.Body.String())
	}
	for rel, payload := range payloads {
		r = httptest.NewRecorder()
		h.ServeHTTP(r, httptest.NewRequest("GET", "/openpak/v1/local/"+strings.Replace(rel, "/", "/news/", 1), nil))
		if r.Code != 200 || !bytes.Equal(r.Body.Bytes(), payload) {
			t.Fatal("local payload mismatch", rel)
		}
	}
	// An article without local.msgpack stays out of the index and the route.
	art := filepath.Join(dir, "openpak_news", "2609100003")
	if err := os.MkdirAll(art, 0755); err != nil {
		t.Fatal(err)
	}
	fixture(t, art, "article.bcat")
	fixture(t, art, "summary.bcat")
	if h, err = Handler(dir, nil, nil); err != nil {
		t.Fatal(err)
	}
	r = httptest.NewRecorder()
	h.ServeHTTP(r, httptest.NewRequest("GET", "/openpak/v1/local/openpak_news/news/2609100003", nil))
	if r.Code != 404 {
		t.Fatal("missing local payload served")
	}
	r = httptest.NewRecorder()
	h.ServeHTTP(r, httptest.NewRequest("GET", "/openpak/v1/local/index", nil))
	if r.Body.String() != want {
		t.Fatal("index changed for an article with no local payload")
	}
	// An empty payload is a build error, not a zero-byte post.
	if err := os.WriteFile(filepath.Join(art, "local.msgpack"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Handler(dir, nil, nil); err == nil {
		t.Fatal("empty local payload accepted")
	}
}

// writeReceptionTree adds what the module's own reception needs: a list per
// channel, one article, a retired channel and the titles answers.
func writeReceptionTree(t *testing.T) string {
	t.Helper()
	dir := writeTree(t, "openpak_news", "openpak_games")
	for _, topic := range []string{"openpak_news", "openpak_games"} {
		fixture(t, filepath.Join(dir, topic), "list.bcat")
	}
	for _, id := range []string{"2609100001", "2609110005"} {
		art := filepath.Join(dir, "openpak_news", id)
		if err := os.MkdirAll(art, 0755); err != nil {
			t.Fatal(err)
		}
		fixture(t, art, "article.bcat")
		fixture(t, art, "summary.bcat")
	}
	for _, rel := range []string{"retired/openpak_old", "retired/openpak_news/2609100002", "titles"} {
		if err := os.MkdirAll(filepath.Join(dir, filepath.FromSlash(rel)), 0755); err != nil {
			t.Fatal(err)
		}
	}
	fixture(t, filepath.Join(dir, "retired", "openpak_old"), "list.bcat")
	fixture(t, filepath.Join(dir, "retired", "openpak_news", "2609100002"), "article.bcat")
	fixture(t, filepath.Join(dir, "titles"), "none.bcat")
	fixture(t, filepath.Join(dir, "titles"), "0100abcd12340000.bcat")
	return dir
}

func get(t *testing.T, h http.Handler, path string, header ...string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("GET", path, nil)
	for i := 0; i+1 < len(header); i += 2 {
		req.Header.Set(header[i], header[i+1])
	}
	r := httptest.NewRecorder()
	h.ServeHTTP(r, req)
	return r
}

func TestReceptionRoutes(t *testing.T) {
	dir := writeReceptionTree(t)
	// The fallback stands for nx-baas: list and titles must never reach it,
	// or a forwarded request would loop back.
	h, err := Handler(dir, nil, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(418) }))
	if err != nil {
		t.Fatal(err)
	}
	read := func(rel string) []byte {
		b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	for path, rel := range map[string]string{
		"/api/nx/v1/list/openpak_news?l=en-GB&c%5B%5D=GB":         "openpak_news/list.bcat",
		"/api/nx/v1/list/openpak_games?l=en-US":                   "openpak_games/list.bcat",
		"/api/nx/v1/list/openpak_old?l=en-GB":                     "retired/openpak_old/list.bcat",
		"/openpak/v1/topics/openpak_news/news/2609100002/expired": "retired/openpak_news/2609100002/article.bcat",
		"/api/nx/v1/titles/0100abcd12340000/topics?c%5B%5D=GB":    "titles/0100abcd12340000.bcat",
		"/api/nx/v1/titles/01006f8002326000/topics":               "titles/none.bcat",
		"/api/nx/v2/latest_news/1?l=en-GB":                        "openpak_news/2609110005/summary.bcat",
		"/openpak/v1/topics/openpak_news/news/2609100001/article": "openpak_news/2609100001/article.bcat",
	} {
		r := get(t, h, path)
		if r.Code != 200 || !bytes.Equal(r.Body.Bytes(), read(rel)) {
			t.Fatalf("%s: code=%d, want %s", path, r.Code, rel)
		}
		if r.Header().Get("ETag") == "" {
			t.Fatalf("%s: no ETag", path)
		}
	}
	for _, path := range []string{
		"/api/nx/v1/list/nx_news?l=en-GB",
		"/api/nx/v1/list/openpak_help",
		"/api/nx/v1/list/openpak_news/extra",
		"/api/nx/v1/titles/0100ABCD12340000/topics",
		"/api/nx/v1/titles/0100abcd1234/topics",
		"/api/nx/v2/latest_news/abc",
	} {
		if r := get(t, h, path); r.Code != 404 {
			t.Fatalf("%s: code=%d, want 404", path, r.Code)
		}
	}
	// Forwarded requests keep the Nintendo Host (or none at all).
	req := httptest.NewRequest("GET", "http://bcat-list-lp1.cdn.nintendo.net/api/nx/v1/list/openpak_news?l=en-GB", nil)
	r := httptest.NewRecorder()
	h.ServeHTTP(r, req)
	if r.Code != 200 {
		t.Fatal("list route depends on Host")
	}
}

func TestETagAndRange(t *testing.T) {
	dir := writeReceptionTree(t)
	h, err := Handler(dir, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{
		"/api/nx/v1/topics/catalog",
		"/api/nx/v1/topics/openpak_news/detail",
		"/api/nx/v1/topics/openpak_news/icon",
		"/api/nx/v2/topics/openpak_news/online_archives",
		"/openpak/v1/topics/openpak_news/news/2609100001/summary",
		"/openpak/v1/topics/openpak_news/news/2609100001/article",
		"/api/nx/v1/list/openpak_news",
		"/api/nx/v1/titles/0100abcd12340000/topics",
	} {
		first := get(t, h, path)
		etag := first.Header().Get("ETag")
		if first.Code != 200 || len(etag) != 34 || etag[0] != '"' {
			t.Fatalf("%s: code=%d etag=%q", path, first.Code, etag)
		}
		if again := get(t, h, path); again.Header().Get("ETag") != etag {
			t.Fatalf("%s: ETag not stable", path)
		}
		if r := get(t, h, path, "If-None-Match", etag); r.Code != 304 || r.Body.Len() != 0 {
			t.Fatalf("%s: If-None-Match code=%d", path, r.Code)
		}
		if r := get(t, h, path, "If-None-Match", `"other"`); r.Code != 200 {
			t.Fatalf("%s: stale If-None-Match code=%d", path, r.Code)
		}
		body := first.Body.Bytes()
		r := get(t, h, path, "Range", "bytes=16-")
		if r.Code != 206 || !bytes.Equal(r.Body.Bytes(), body[16:]) {
			t.Fatalf("%s: Range code=%d", path, r.Code)
		}
		// A resume against changed bytes must restart from zero.
		if r := get(t, h, path, "Range", "bytes=16-", "If-Range", etag); r.Code != 206 {
			t.Fatalf("%s: matching If-Range code=%d", path, r.Code)
		}
		if r := get(t, h, path, "Range", "bytes=16-", "If-Range", `"other"`); r.Code != 200 || !bytes.Equal(r.Body.Bytes(), body) {
			t.Fatalf("%s: stale If-Range code=%d", path, r.Code)
		}
	}
	// Different bytes, different ETag.
	a := get(t, h, "/api/nx/v1/list/openpak_news").Header().Get("ETag")
	b := get(t, h, "/api/nx/v1/list/openpak_games").Header().Get("ETag")
	if a == b {
		t.Fatal("distinct fixtures share an ETag")
	}
}

func TestLiveAndRetiredListConflict(t *testing.T) {
	dir := writeReceptionTree(t)
	if err := os.MkdirAll(filepath.Join(dir, "retired", "openpak_games"), 0755); err != nil {
		t.Fatal(err)
	}
	fixture(t, filepath.Join(dir, "retired", "openpak_games"), "list.bcat")
	if _, err := Handler(dir, nil, nil); err == nil {
		t.Fatal("topic with a live and a retired list accepted")
	}
}
