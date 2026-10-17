package main

// The News channels' signed tree. News publishes an unsigned seed; this service pulls the
// current build, signs it with its own keys, swaps it in atomically and tells the consoles that
// follow each changed topic (push.go). Pulls happen when news calls POST /internal/bcat/sync
// after a publish or rollback and, as the net under a lost call, every NX_BCAT_POLL.

import (
	"archive/tar"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"nx-bcat/newsexperiment"
)

type tree struct {
	dir      string // builds/r<revision>/{seed,signed}, state.json
	static   string // pre-signed tree served until news publishes a build ("" = none)
	news     *upstream
	keys     *keys
	sw       *newsexperiment.Switch
	fallback http.Handler
	push     func(topics []string, revision int64)

	mu       sync.Mutex
	revision int64             // loaded; 0 = none
	lists    map[string]string // topic -> list sha256 of the loaded build; nil = unknown
}

type treeState struct {
	Revision int64             `json:"revision"`
	Lists    map[string]string `json:"lists"`
}

func (t *tree) revDir(rev int64) string { return filepath.Join(t.dir, fmt.Sprintf("r%d", rev)) }

// load serves the build the last run ended on, so a restart (or news being down) keeps the
// consoles' lists answering.
func (t *tree) load() {
	raw, err := os.ReadFile(filepath.Join(t.dir, "state.json"))
	var st treeState
	if err != nil || json.Unmarshal(raw, &st) != nil || st.Revision == 0 {
		t.loadStatic()
		return
	}
	h, err := newsexperiment.Handler(filepath.Join(t.revDir(st.Revision), "signed"), nil, t.fallback)
	if err != nil {
		log.Printf("%s stored build r%d does not load: %v", logPrefix, st.Revision, err)
		t.loadStatic()
		return
	}
	t.sw.Store(h)
	t.revision, t.lists = st.Revision, st.Lists
	log.Printf("%s serving stored build r%d", logPrefix, st.Revision)
}

// loadStatic serves the pre-signed tree news was serving before its first publish
// (NX_BCAT_STATIC_DIR, the file-based cdn-vNNN builds). A later published build replaces it.
func (t *tree) loadStatic() {
	if t.static == "" {
		return
	}
	h, err := newsexperiment.Handler(t.static, nil, t.fallback)
	if err != nil {
		log.Printf("%s static tree %s: %v", logPrefix, t.static, err)
		return
	}
	t.sw.Store(h)
	log.Printf("%s serving static tree %s", logPrefix, t.static)
}

// sync pulls the build news serves now, if it is not the one already loaded.
func (t *tree) sync(ctx context.Context) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	resp, err := t.news.get(ctx, "/admin/v1/bcat/current")
	if err != nil {
		return err
	}
	var cur struct {
		Build    int   `json:"build"`
		Revision int64 `json:"revision"`
	}
	err = decodeOK(resp, &cur)
	if err != nil {
		return err
	}
	if cur.Build == 0 || cur.Revision == t.revision {
		return nil // nothing published yet / already loaded
	}
	dir := t.revDir(cur.Revision)
	seed, signed := filepath.Join(dir, "seed"), filepath.Join(dir, "signed")
	os.RemoveAll(dir)
	ok := false
	defer func() {
		if !ok {
			os.RemoveAll(dir)
		}
	}()
	resp, err = t.news.get(ctx, fmt.Sprintf("/admin/v1/bcat/builds/%d/seed.tar", cur.Build))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("seed of build %d: %s", cur.Build, resp.Status)
	}
	if err := untar(resp.Body, seed); err != nil {
		return fmt.Errorf("seed of build %d: %w", cur.Build, err)
	}
	lists, err := seedLists(seed)
	if err != nil {
		return err
	}
	n, err := t.keys.signTree(seed, signed)
	if err != nil {
		return fmt.Errorf("sign: %w", err)
	}
	h, err := newsexperiment.Handler(signed, nil, t.fallback)
	if err != nil {
		return fmt.Errorf("load signed tree: %w", err)
	}
	changed := changedTopics(t.lists, lists)
	prev := t.revision
	st, _ := json.Marshal(treeState{Revision: cur.Revision, Lists: lists})
	if err := os.WriteFile(filepath.Join(t.dir, "state.json"), st, 0644); err != nil {
		return err
	}
	ok = true
	t.sw.Store(h)
	first := t.lists == nil
	t.revision, t.lists = cur.Revision, lists
	log.Printf("%s now serving build %d (r%d, %d containers); changed topics %v", logPrefix, cur.Build, cur.Revision, n, changed)
	if prev != 0 {
		os.RemoveAll(t.revDir(prev))
	}
	// A first load (new service, empty data dir) has nothing to compare with: every
	// console already fetches at boot, so pushing every topic would only be noise.
	if !first && t.push != nil && len(changed) > 0 {
		t.push(changed, cur.Revision)
	}
	return nil
}

func decodeOK(resp *http.Response, v any) error {
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("news: %s", resp.Status)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(v)
}

// untar extracts regular files only, refusing any path that leaves dst.
func untar(r io.Reader, dst string) error {
	tr := tar.NewReader(r)
	var total int64
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if h.Typeflag != tar.TypeReg {
			continue
		}
		name := filepath.Clean(filepath.FromSlash(h.Name))
		if name == "." || filepath.IsAbs(name) || name == ".." || strings.HasPrefix(name, ".."+string(filepath.Separator)) {
			return fmt.Errorf("unsafe path %q", h.Name)
		}
		if total += h.Size; total > 256<<20 {
			return errors.New("seed larger than 256 MiB")
		}
		path := filepath.Join(dst, name)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			return err
		}
		f, err := os.Create(path)
		if err != nil {
			return err
		}
		_, err = io.Copy(f, io.LimitReader(tr, h.Size))
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return err
		}
	}
}

// seedLists reads the per-topic list digests out of the seed's manifest.json (the subset of
// news internal/newsseed's Manifest that decides whether a topic changed).
func seedLists(seed string) (map[string]string, error) {
	raw, err := os.ReadFile(filepath.Join(seed, "manifest.json"))
	if err != nil {
		return nil, err
	}
	var m struct {
		Channels []struct {
			TopicID string `json:"topic_id"`
			Files   map[string]struct {
				SHA256 string `json:"sha256"`
			} `json:"files"`
		} `json:"channels"`
		Removed []struct {
			TopicID string `json:"topic_id"`
			List    *struct {
				SHA256 string `json:"sha256"`
			} `json:"list"`
		} `json:"removed_channels"`
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, ch := range m.Channels {
		out[ch.TopicID] = ch.Files["list"].SHA256
	}
	for _, ch := range m.Removed {
		if ch.List != nil {
			out[ch.TopicID] = ch.List.SHA256
		}
	}
	return out, nil
}

// changedTopics lists every topic whose bcat-list payload differs between two builds.
func changedTopics(old, cur map[string]string) []string {
	out := []string{}
	for topic, sum := range cur {
		if old[topic] != sum {
			out = append(out, topic)
		}
	}
	sort.Strings(out)
	return out
}

// run keeps the tree current: a pull now, then every poll, retrying quickly while news is down.
func (t *tree) run(ctx context.Context, poll time.Duration) {
	wait := time.Duration(0)
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		if err := t.sync(ctx); err != nil {
			log.Printf("%s sync: %v", logPrefix, err)
			wait = 15 * time.Second
		} else {
			wait = poll
		}
	}
}
