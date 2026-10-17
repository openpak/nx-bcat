package main

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"nx-bcat/newsexperiment"
	"nx-bcat/newsformat"
)

func seedTar(t *testing.T, lists map[string]string) []byte {
	t.Helper()
	cat, err := newsformat.Encode([]any{})
	if err != nil {
		t.Fatal(err)
	}
	type ch struct {
		TopicID string                       `json:"topic_id"`
		Files   map[string]map[string]string `json:"files"`
	}
	var man struct {
		Channels []ch `json:"channels"`
	}
	for topic, sum := range lists {
		man.Channels = append(man.Channels, ch{topic, map[string]map[string]string{"list": {"sha256": sum}}})
	}
	raw, _ := json.Marshal(man)
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for name, b := range map[string][]byte{"catalog.msgpack": cat, "manifest.json": raw} {
		tw.WriteHeader(&tar.Header{Name: name, Mode: 0644, Size: int64(len(b)), Typeflag: tar.TypeReg})
		tw.Write(b)
	}
	tw.Close()
	return buf.Bytes()
}

// A published build is pulled, signed, served, and its changed topics pushed -- but not on
// the first load, and not when the revision is the one already loaded.
func TestSyncPullsSignsAndPushesChangedTopics(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	build, revision := 1, int64(1)
	lists := map[string]string{"openpak_news": "aa"}
	news := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Internal-Key") != "k" {
			http.Error(w, "no", 401)
			return
		}
		switch r.URL.Path {
		case "/admin/v1/bcat/current":
			json.NewEncoder(w).Encode(map[string]any{"build": build, "revision": revision})
		case "/admin/v1/bcat/builds/1/seed.tar", "/admin/v1/bcat/builds/2/seed.tar":
			w.Write(seedTar(t, lists))
		default:
			http.NotFound(w, r)
		}
	}))
	defer news.Close()
	var pushed [][]string
	tr := &tree{dir: t.TempDir(), news: newUpstream(news.URL, "k"),
		keys: &keys{signing: key, verify: &key.PublicKey, aes: make([]byte, 16)},
		sw:   &newsexperiment.Switch{},
		push: func(topics []string, _ int64) { pushed = append(pushed, topics) }}
	ctx := context.Background()
	if err := tr.sync(ctx); err != nil {
		t.Fatal(err)
	}
	if tr.revision != 1 || len(pushed) != 0 {
		t.Fatalf("first load: revision %d, pushed %v", tr.revision, pushed)
	}
	rec := httptest.NewRecorder()
	tr.sw.ServeHTTP(rec, httptest.NewRequest("GET", "/api/nx/v1/topics/catalog", nil))
	if rec.Code != 200 {
		t.Fatalf("catalog: %d", rec.Code)
	}
	if err := tr.sync(ctx); err != nil || len(pushed) != 0 {
		t.Fatalf("same revision: %v pushed %v", err, pushed)
	}
	build, revision, lists = 2, 2, map[string]string{"openpak_news": "bb"}
	if err := tr.sync(ctx); err != nil {
		t.Fatal(err)
	}
	if len(pushed) != 1 || pushed[0][0] != "openpak_news" {
		t.Fatalf("pushed %v", pushed)
	}
}
