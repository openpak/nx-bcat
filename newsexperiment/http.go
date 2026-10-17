// SPDX-License-Identifier: AGPL-3.0-only
// Package newsexperiment serves pre-signed original fixtures for hardware tests.
//
// The fixture directory layout mirrors cmd/newsseed's build output after
// signing (tools/news-cdn/sign-seed.sh):
//
//	catalog.bcat                    all channels, one signed catalog
//	<topic_id>/detail.bcat          per-channel detail record
//	<topic_id>/icon.bcat            per-channel icon (JPEG plaintext)
//	<topic_id>/archives.bcat        per-channel archives response
//	<topic_id>/list.bcat            bcat-list response (optional in older trees)
//	<topic_id>/<news_id>/summary.bcat, article.bcat   signed article pair
//	<topic_id>/<news_id>/local.msgpack                plaintext post payload
//	retired/<topic_id>/list.bcat                      expired list of a removed channel
//	retired/<topic_id>/<news_id>/article.bcat         expired replacement article
//	titles/<title_id>.bcat, titles/none.bcat          auto-subscription topic arrays
//
// The News module's own fetch is the bcat-list route
// (/api/nx/v1/list/<topic>): it names each article's data URL and exact
// size. nx-baas answers bcat-list for every other topic and forwards the
// OpenPak ones (and titles/<tid>/topics) here with path and query intact,
// so every route matches on path alone, whatever the Host header says.
//
// Every response carries a strong ETag (SHA-256 of the body) and goes
// through http.ServeContent, which answers If-None-Match with 304 and
// honours Range/If-Range: the module sends both.
//
// local.msgpack is the same decoded article payload, served unwrapped for
// the homebrew sync helper: newsPostLocalNews takes MessagePack directly,
// so that path needs no container, no key patch and no DNS redirect.
//
// Routes are derived from the directory, so a channel appears as soon as
// its three fixtures exist; the catalog is never decoded here because the
// CDN holds no keys. A tree with no channel directories serves the (empty)
// catalog alone — the builder's answer to "every channel retired/removed".
// Unknown routes fail explicitly rather than inventing a successful
// response; the list, titles and latest_news prefixes never reach the
// fallback, because the fallback is the nx-baas that forwards them here.
package newsexperiment

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"nx-bcat/bcatcontainer"
)

var (
	reTopic   = regexp.MustCompile(`^[a-z][a-z0-9_]{2,63}$`)
	reTitleID = regexp.MustCompile(`^[0-9a-f]{16}$`)
	reDigits  = regexp.MustCompile(`^[0-9]{1,20}$`)
)

// reserved root directories are not topics.
var reserved = map[string]bool{"retired": true, "titles": true}

// Handler keeps keys off the CDN and loads a bounded set of immutable fixtures.
func Handler(dir string, logger *slog.Logger, fallback http.Handler) (http.Handler, error) {
	if logger == nil {
		logger = slog.Default()
	}
	catalog, err := loadFixture(filepath.Join(dir, "catalog.bcat"))
	if err != nil {
		return nil, fmt.Errorf("catalog: %w", err)
	}
	// The pre-seed single-channel layout must not be mounted silently: it
	// would serve a catalog the per-channel routes cannot back.
	for _, legacy := range []string{"detail.bcat", "icon.bcat", "archives.bcat"} {
		if _, err := os.Stat(filepath.Join(dir, legacy)); err == nil {
			return nil, fmt.Errorf("legacy single-channel fixture %s found; rebuild with cmd/newsseed and sign with tools/news-cdn/sign-seed.sh", legacy)
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	type channel struct {
		detail, icon, archives, list []byte
		plain                        map[string][]byte // detail, icon, archives: decoded, for the local routes
		summaries                    map[string][]byte // news id -> decoded summary
		articles                     map[string]map[string][]byte
		local                        map[string][]byte
	}
	channels := map[string]channel{}
	for _, e := range entries {
		if !e.IsDir() || reserved[e.Name()] {
			continue
		}
		topic := e.Name()
		if !reTopic.MatchString(topic) {
			return nil, fmt.Errorf("fixture directory %q is not a valid topic id", topic)
		}
		var ch channel
		for _, f := range []struct {
			name string
			dst  *[]byte
		}{{"detail.bcat", &ch.detail}, {"icon.bcat", &ch.icon}, {"archives.bcat", &ch.archives}} {
			b, err := loadFixture(filepath.Join(dir, topic, f.name))
			if err != nil {
				return nil, fmt.Errorf("channel %s: %w", topic, err)
			}
			*f.dst = b
		}
		if ch.list, err = optionalFixture(filepath.Join(dir, topic, "list.bcat")); err != nil {
			return nil, fmt.Errorf("channel %s: %w", topic, err)
		}
		if ch.list == nil {
			logger.Warn("channel has no list fixture; the News module cannot receive it", "topic", topic)
		}
		ch.plain = map[string][]byte{}
		for _, name := range []string{"detail", "icon", "archives"} {
			b, err := optionalPlain(filepath.Join(dir, topic, name+".plain"))
			if err != nil {
				return nil, fmt.Errorf("channel %s: %w", topic, err)
			}
			if b != nil {
				ch.plain[name] = b
			}
		}
		ch.articles = map[string]map[string][]byte{}
		ch.local = map[string][]byte{}
		ch.summaries = map[string][]byte{}
		children, err := os.ReadDir(filepath.Join(dir, topic))
		if err != nil {
			return nil, err
		}
		for _, child := range children {
			if !child.IsDir() {
				continue
			}
			if !validNewsID(child.Name()) {
				return nil, fmt.Errorf("invalid article directory %s/%s", topic, child.Name())
			}
			resources := map[string][]byte{}
			for _, resource := range []string{"summary", "article"} {
				data, err := loadFixture(filepath.Join(dir, topic, child.Name(), resource+".bcat"))
				if err != nil {
					return nil, fmt.Errorf("article %s/%s: %w", topic, child.Name(), err)
				}
				resources[resource] = data
			}
			ch.articles[child.Name()] = resources
			summary, err := optionalPlain(filepath.Join(dir, topic, child.Name(), "summary.plain"))
			if err != nil {
				return nil, fmt.Errorf("article %s/%s: %w", topic, child.Name(), err)
			}
			if summary != nil {
				ch.summaries[child.Name()] = summary
			}
			// Optional: the plaintext payload for local posting.
			payload, err := os.ReadFile(filepath.Join(dir, topic, child.Name(), "local.msgpack"))
			switch {
			case errors.Is(err, fs.ErrNotExist):
			case err != nil:
				return nil, fmt.Errorf("local payload %s/%s: %w", topic, child.Name(), err)
			case len(payload) == 0 || len(payload) > bcatcontainer.MaxPayload:
				return nil, fmt.Errorf("invalid local payload size: %s/%s", topic, child.Name())
			default:
				ch.local[child.Name()] = payload
			}
		}
		channels[topic] = ch
	}
	// An empty lineup is a legitimate state (every channel retired or
	// removed): the catalog fixture itself describes it, so serve it. Its
	// acceptance by the Find Channels UI on hardware is untested.
	if len(channels) == 0 {
		logger.Warn("no channel fixture directories; serving an empty catalog")
	}
	retired, err := loadRetired(filepath.Join(dir, "retired"))
	if err != nil {
		return nil, err
	}
	titles, err := loadTitles(filepath.Join(dir, "titles"))
	if err != nil {
		return nil, err
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/nx/v1/topics/catalog", serveFixture(catalog, "catalog", logger))
	// The bcat-list, titles and latest_news prefixes answer 404 themselves
	// instead of reaching the fallback (see the package comment).
	notFound := func(w http.ResponseWriter, r *http.Request) {
		logger.Info("news route not served", "method", r.Method, "path", r.URL.Path)
		http.NotFound(w, r)
	}
	mux.HandleFunc("GET /api/nx/v1/list/", notFound)
	mux.HandleFunc("GET /api/nx/v1/titles/", notFound)
	mux.HandleFunc("GET /api/nx/v2/latest_news/", notFound)
	var newestID uint64
	var newestSummary []byte
	for topic, ch := range channels {
		mux.HandleFunc("GET /api/nx/v1/topics/"+topic+"/detail", serveFixture(ch.detail, topic+"/detail", logger))
		mux.HandleFunc("GET /api/nx/v1/topics/"+topic+"/icon", serveFixture(ch.icon, topic+"/icon", logger))
		mux.HandleFunc("GET /api/nx/v2/topics/"+topic+"/online_archives", serveFixture(ch.archives, topic+"/archives", logger))
		if ch.list != nil {
			if _, dup := retired.lists[topic]; dup {
				return nil, fmt.Errorf("topic %s has both a live and a retired list", topic)
			}
			mux.HandleFunc("GET /api/nx/v1/list/"+topic, serveFixture(ch.list, topic+"/list", logger))
		}
		// v1 archives and <id>/article: kept because a 2026-09-16 capture
		// showed requests for them, but 22.5.0 builds neither path — not
		// qlaunch, not the bcat module. The module's article fetches go to
		// the URLs its list names (/openpak/v1/.../article below).
		mux.HandleFunc("GET /api/nx/v1/topics/"+topic+"/archives", serveFixture(ch.archives, topic+"/archives", logger))
		for id, resources := range ch.articles {
			for name, data := range resources {
				label := topic + "/" + id + "/" + name
				mux.HandleFunc("GET /openpak/v1/topics/"+topic+"/news/"+id+"/"+name, serveFixture(data, label, logger))
				if name == "article" {
					mux.HandleFunc("GET /api/nx/v1/topics/"+topic+"/"+id+"/article", serveFixture(data, label, logger))
				}
			}
			// News ids are YYMMDD + sequence, so the largest is the newest.
			if n, _ := strconv.ParseUint(id, 10, 32); n > newestID {
				newestID, newestSummary = n, resources["summary"]
			}
		}
		for id, payload := range ch.local {
			mux.HandleFunc("GET /openpak/v1/local/"+topic+"/news/"+id, serveFixture(payload, topic+"/"+id+"/local", logger))
		}
		for id, payload := range ch.summaries {
			mux.HandleFunc("GET /openpak/v1/local/"+topic+"/news/"+id+"/summary", serveFixture(payload, topic+"/"+id+"/summary/local", logger))
		}
		for name, payload := range ch.plain {
			mux.HandleFunc("GET /openpak/v1/local/"+topic+"/"+name, serveFixture(payload, topic+"/"+name+"/local", logger))
		}
	}
	for topic, list := range retired.lists {
		mux.HandleFunc("GET /api/nx/v1/list/"+topic, serveFixture(list, "retired/"+topic+"/list", logger))
	}
	for rel, data := range retired.articles {
		topic, id, _ := strings.Cut(rel, "/")
		mux.HandleFunc("GET /openpak/v1/topics/"+topic+"/news/"+id+"/expired", serveFixture(data, "retired/"+rel, logger))
	}
	if titles.none != nil {
		mux.HandleFunc("GET /api/nx/v1/titles/{tid}/topics", func(w http.ResponseWriter, r *http.Request) {
			tid := r.PathValue("tid")
			if !reTitleID.MatchString(tid) {
				notFound(w, r)
				return
			}
			data, label := titles.byID[tid], "titles/"+tid
			if data == nil {
				data, label = titles.none, "titles/none"
			}
			serveFixture(data, label, logger)(w, r)
		})
	}
	// latest_news/<n>: qlaunch's own request (trigger unknown). Answered
	// with the newest OpenPak article's summary card, whatever n is.
	if newestSummary != nil {
		label := "latest_news/" + strconv.FormatUint(newestID, 10)
		mux.HandleFunc("GET /api/nx/v2/latest_news/{n}", func(w http.ResponseWriter, r *http.Request) {
			if !reDigits.MatchString(r.PathValue("n")) {
				notFound(w, r)
				return
			}
			serveFixture(newestSummary, label, logger)(w, r)
		})
	}
	// Sync index for the homebrew helper: one "<topic> <news_id> <bytes>"
	// line per postable article, sorted so the bytes are stable.
	var index bytes.Buffer
	topics := make([]string, 0, len(channels))
	for topic := range channels {
		topics = append(topics, topic)
	}
	sort.Strings(topics)
	for _, topic := range topics {
		ids := make([]string, 0, len(channels[topic].local))
		for id := range channels[topic].local {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for _, id := range ids {
			fmt.Fprintf(&index, "%s %s %d\n", topic, id, len(channels[topic].local[id]))
		}
	}
	mux.HandleFunc("GET /openpak/v1/local/index", serveFixture(index.Bytes(), "local/index", logger))
	// The decoded catalog, for a client without the News key (an emulator
	// answering the firmware's catalog download itself). Optional: a tree
	// signed before the .plain copies existed has none.
	catalogPlain, err := optionalPlain(filepath.Join(dir, "catalog.plain"))
	if err != nil {
		return nil, fmt.Errorf("catalog: %w", err)
	}
	if catalogPlain != nil {
		mux.HandleFunc("GET /openpak/v1/local/catalog", serveFixture(catalogPlain, "local/catalog", logger))
	}
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok\n")) })
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		logger.Info("unhandled news route", "method", r.Method, "path", r.URL.Path)
		if fallback != nil {
			fallback.ServeHTTP(w, r)
			return
		}
		http.NotFound(w, r)
	})
	return mux, nil
}

// loadFixture reads one container and validates the header fields the
// console's BCAT loader enforces, so a malformed file fails at start-up.
func loadFixture(path string) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() <= bcatcontainer.HeaderSize || info.Size() > bcatcontainer.HeaderSize+bcatcontainer.MaxPayload {
		return nil, fmt.Errorf("invalid fixture size: %s", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(data) <= bcatcontainer.HeaderSize || len(data) > bcatcontainer.HeaderSize+bcatcontainer.MaxPayload ||
		string(data[:4]) != "bcat" || data[4] != 1 || data[5] < 1 || data[5] > 3 || data[6] != 1 || data[7] > 15 {
		return nil, fmt.Errorf("invalid container header: %s", path)
	}
	return data, nil
}

// optionalFixture is loadFixture for a file that may be absent (nil, nil).
func optionalFixture(path string) ([]byte, error) {
	data, err := loadFixture(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	return data, err
}

// optionalPlain reads the decoded payload the signer leaves beside a
// container (<name>.plain); nil when the tree has none.
func optionalPlain(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err == nil && (len(data) == 0 || len(data) > bcatcontainer.MaxPayload) {
		return nil, fmt.Errorf("invalid plain payload size: %s", path)
	}
	return data, err
}

func validNewsID(name string) bool {
	id, err := strconv.ParseUint(name, 10, 32)
	return err == nil && id != 0 && strconv.FormatUint(id, 10) == name
}

type retiredTree struct {
	lists    map[string][]byte // topic -> expired list
	articles map[string][]byte // "topic/id" -> expired replacement
}

// loadRetired reads retired/: an expired list per removed channel and the
// expired replacement of every retired article. The tree is optional.
func loadRetired(root string) (retiredTree, error) {
	t := retiredTree{lists: map[string][]byte{}, articles: map[string][]byte{}}
	topics, err := os.ReadDir(root)
	if errors.Is(err, fs.ErrNotExist) {
		return t, nil
	}
	if err != nil {
		return t, err
	}
	for _, e := range topics {
		if !e.IsDir() {
			continue
		}
		topic := e.Name()
		if !reTopic.MatchString(topic) || reserved[topic] {
			return t, fmt.Errorf("retired directory %q is not a valid topic id", topic)
		}
		list, err := optionalFixture(filepath.Join(root, topic, "list.bcat"))
		if err != nil {
			return t, fmt.Errorf("retired %s: %w", topic, err)
		}
		if list != nil {
			t.lists[topic] = list
		}
		children, err := os.ReadDir(filepath.Join(root, topic))
		if err != nil {
			return t, err
		}
		for _, child := range children {
			if !child.IsDir() {
				continue
			}
			if !validNewsID(child.Name()) {
				return t, fmt.Errorf("invalid retired article directory %s/%s", topic, child.Name())
			}
			data, err := loadFixture(filepath.Join(root, topic, child.Name(), "article.bcat"))
			if err != nil {
				return t, fmt.Errorf("retired article %s/%s: %w", topic, child.Name(), err)
			}
			t.articles[topic+"/"+child.Name()] = data
		}
	}
	return t, nil
}

type titleTree struct {
	none []byte            // answer for every unmapped title
	byID map[string][]byte // title id -> topic array
}

// loadTitles reads titles/. Without none.bcat the titles route answers 404
// (an older tree); with it, unmapped titles get that empty array.
func loadTitles(root string) (titleTree, error) {
	t := titleTree{byID: map[string][]byte{}}
	entries, err := os.ReadDir(root)
	if errors.Is(err, fs.ErrNotExist) {
		return t, nil
	}
	if err != nil {
		return t, err
	}
	for _, e := range entries {
		name, ok := strings.CutSuffix(e.Name(), ".bcat")
		if e.IsDir() || !ok {
			continue
		}
		if name != "none" && !reTitleID.MatchString(name) {
			return t, fmt.Errorf("titles/%s is not a title id", e.Name())
		}
		data, err := loadFixture(filepath.Join(root, e.Name()))
		if err != nil {
			return t, err
		}
		if name == "none" {
			t.none = data
		} else {
			t.byID[name] = data
		}
	}
	if t.none == nil && len(t.byID) > 0 {
		return t, fmt.Errorf("titles/none.bcat missing: unmapped titles would have no answer")
	}
	return t, nil
}

// serveFixture answers with a strong ETag over the exact bytes, so the
// module's If-None-Match revalidation gets 304 while nothing changed and
// If-Range/Range resumes only against the same bytes.
func serveFixture(data []byte, label string, logger *slog.Logger) http.HandlerFunc {
	sum := sha256.Sum256(data)
	etag := `"` + hex.EncodeToString(sum[:16]) + `"`
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("ETag", etag)
		logger.Info("news fixture", "method", r.Method, "fixture", label, "bytes", len(data))
		http.ServeContent(w, r, label, time.Time{}, bytes.NewReader(data))
	}
}
