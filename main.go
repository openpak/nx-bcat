// nx-bcat: the Switch's BCAT module for OpenPak -- signed News channels, per-title game data
// lists and files, and the push that tells a console a topic changed -- split out of nx-baas
// and the news service (README.md).
//
// It owns the bcat hostnames (bcat-list-<env>, bcat-topics-<env>, bcat-data-<env>.cdn.nintendo.net)
// and the only copy of the BCAT signing and News AES keys. Content stays with the news service:
// the unsigned News seed and the game datasets are pulled from its internal API (source.go,
// tree.go). Two listeners:
//
//	NX_ADDR      TLS for consoles (Traefik passes the SNIs through), certificates from the
//	             OpenPak CA in NX_CA_DIR (nx-common/nxtls)
//	NX_API_ADDR  plain HTTP on the openpak network: /internal/bcat/sync and /internal/news/push,
//	             the console routes again for what nx-baas forwards, the homebrew's local routes
package main

import (
	"context"
	"crypto/tls"
	"log"
	"net/http"
	"os"
	"os/signal"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/openpak/nx-common/internalkey"
	"github.com/openpak/nx-common/nxapi"
	"github.com/openpak/nx-common/nxhttp"
	"github.com/openpak/nx-common/nxtls"

	"nx-bcat/nativebcat"
	"nx-bcat/newsexperiment"
)

var fixedNames = []string{"bcat-list-lp1.cdn.nintendo.net", "bcat-topics-lp1.cdn.nintendo.net", "bcat-data-lp1.cdn.nintendo.net"}

var debug = os.Getenv("NX_DEBUG") != ""

var reNativeList = regexp.MustCompile(`^/api/nx/v1/list/nx_data_[0-9a-f]{16}$`)

type server struct {
	tree   *tree
	native http.Handler // nil without datasets configured
	push   *pusher
}

// ServeHTTP answers the bcat hosts, as nx-baas's bcat() did; any other host is misrouted.
func (s *server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	host, p := hostOf(r), r.URL.Path
	log.Printf("%s %s %s%s", logPrefix, r.Method, host, r.URL.RequestURI())
	switch {
	case p == "/healthz":
		writeJSON(w, 200, map[string]any{"status": "ok", "service": "nx-bcat"})
	case !strings.HasPrefix(host, "bcat-"):
		nxhttp.Misrouted(logPrefix, w, r)
	// Game data: the per-title list and our own signed file URLs.
	case strings.HasPrefix(host, "bcat-data-") && strings.HasPrefix(p, "/bcat/"),
		strings.HasPrefix(host, "bcat-list-") && reNativeList.MatchString(p):
		if s.native == nil {
			legacy(w, r)
			return
		}
		s.native.ServeHTTP(w, r)
	// News: the OpenPak topics and every topic/title lookup are the signed tree's.
	case strings.HasPrefix(host, "bcat-list-") && strings.HasPrefix(p, "/api/nx/v1/list/openpak_"),
		strings.HasPrefix(host, "bcat-topics-"):
		s.tree.sw.ServeHTTP(w, r)
	default:
		legacy(w, r)
	}
}

// internalAPI: console hosts (nx-baas's forwards) go to ServeHTTP; every other Host reaches
// the /internal routes and the plaintext routes the homebrew's sync helper reads.
func (s *server) internalAPI() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/internal/bcat/sync", internalkey.Guard(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			apiErr(w, http.StatusMethodNotAllowed, "POST required")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), time.Minute)
		defer cancel()
		if err := s.tree.sync(ctx); err != nil {
			log.Printf("%s sync: %v", logPrefix, err)
			apiErr(w, http.StatusBadGateway, "sync failed")
			return
		}
		s.tree.mu.Lock()
		rev := s.tree.revision
		s.tree.mu.Unlock()
		writeJSON(w, 200, map[string]any{"revision": rev})
	})))
	mux.HandleFunc("/internal/news/push", s.push.handle)
	mux.Handle("/openpak/", s.tree.sw)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"status": "ok", "service": "nx-bcat"})
	})
	return nxhttp.SplitListener(s, mux)
}

func main() {
	data := getenv("NX_DATA", "/data")
	penneURL := os.Getenv("NX_PENNE_URL")
	if penneURL == "" {
		penneURL = os.Getenv("NX_CORE_URL") // nx-baas serves (or forwards) the penne API too
	}
	adminURL := os.Getenv("NEWS_ADMIN_URL")
	if penneURL == "" || adminURL == "" {
		log.Fatalf("%s NX_PENNE_URL (or NX_CORE_URL) and NEWS_ADMIN_URL are required", logPrefix)
	}
	k, err := loadKeys(getenv("BCAT_SIGNING_KEY", "/run/secrets/bcat-signing-key"), getenv("BCAT_AES_KEY", "/run/secrets/bcat-aes-key"))
	if err != nil {
		log.Fatalf("%s keys: %v", logPrefix, err)
	}
	p := &pusher{
		penne:     nxapi.New(penneURL),
		receiver:  getenv("NX_PENNE_NEWS_RECEIVER", "bcat"),
		waitRange: jsonNumber(os.Getenv("NX_BCAT_PUSH_WAIT_RANGE")),
	}
	s := &server{push: p}
	s.tree = &tree{
		dir: data + "/builds", static: os.Getenv("NX_BCAT_STATIC_DIR"), news: newUpstream(adminURL, os.Getenv("NEWS_ADMIN_KEY")), keys: k,
		sw: &newsexperiment.Switch{}, fallback: http.HandlerFunc(legacy), push: p.pushAll,
	}
	if err := os.MkdirAll(s.tree.dir, 0755); err != nil {
		log.Fatal(err)
	}
	s.tree.load()
	if u := os.Getenv("NEWS_DATASET_URL"); u != "" {
		svc, err := nativebcat.NewService(datasetSource{newUpstream(u, internalkey.Key())}, k.signing,
			getenv("NX_BCAT_DATA_ORIGIN", "https://bcat-data-lp1.cdn.nintendo.net"))
		if err != nil {
			log.Fatalf("%s native data: %v", logPrefix, err)
		}
		mux := http.NewServeMux()
		svc.RegisterRoutes(mux)
		s.native = mux
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	poll, err := time.ParseDuration(getenv("NX_BCAT_POLL", "5m"))
	if err != nil {
		log.Fatalf("%s NX_BCAT_POLL: %v", logPrefix, err)
	}
	go s.tree.run(ctx, poll)
	go func() {
		addr := getenv("NX_API_ADDR", ":20070")
		log.Printf("%s internal API listening %s", logPrefix, addr)
		log.Fatal(http.ListenAndServe(addr, s.internalAPI()))
	}()
	srv := &http.Server{
		Addr: getenv("NX_ADDR", ":21000"), Handler: s,
		TLSConfig: nxtls.ServerConfig(logPrefix, fixedNames, getenv("NX_CA_DIR", "/data/ca"), debug),
		// HTTP/2 disallows some of the legacy TLS suites the console needs.
		TLSNextProto:      map[string]func(*http.Server, *tls.Conn, http.Handler){},
		ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 16 << 10,
	}
	log.Printf("%s listening %s; news %s; penne %s; game data %v", logPrefix, srv.Addr, adminURL, penneURL, s.native != nil)
	log.Fatal(srv.ListenAndServeTLS("", ""))
}
