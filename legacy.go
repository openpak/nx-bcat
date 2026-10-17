package main

// What nx-baas's bcat.go answered for a bcat route nothing publishes: in service, nothing to
// fetch. A title with no BCAT container and a topic with no issues are exactly what an empty
// directories list means. Anything else on the hosts is logged with its path rather than
// answered with a plausible-looking empty object: an unmapped BCAT route that returns 200 is
// indistinguishable from one that works.

import (
	"crypto/sha1"
	"encoding/hex"
	"log"
	"net/http"
	"strings"

	"nx-bcat/newsformat"
)

func emptyDoc(w http.ResponseWriter, r *http.Request, doc newsformat.Map) {
	b, err := newsformat.Encode(doc)
	if err != nil {
		http.Error(w, "encode", http.StatusInternalServerError)
		return
	}
	sum := sha1.Sum(b)
	etag := `"` + hex.EncodeToString(sum[:8]) + `"`
	w.Header().Set("ETag", etag)
	if m := r.Header.Get("If-None-Match"); m != "" && strings.Contains(m, etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Type", "application/x-msgpack")
	w.Write(b)
}

func legacy(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Path
	switch {
	// Per-title topic document: $.topic_id, $.na_required, $.test_distribution,
	// $.service_status, $.directories[0].data_list[].
	case strings.HasSuffix(p, "/topics"):
		log.Printf("%s topics %s (empty)", logPrefix, p)
		emptyDoc(w, r, newsformat.Map{
			{Key: "service_status", Value: "in_service"},
			{Key: "topic_id", Value: ""},
			{Key: "na_required", Value: false},
			{Key: "test_distribution", Value: false},
			{Key: "directories", Value: []any{}},
		})
	// Data list: $.required_application_version, $.directories[*].
	case strings.Contains(p, "/v1/list/"):
		log.Printf("%s list %s (empty)", logPrefix, p[strings.LastIndex(p, "/")+1:])
		emptyDoc(w, r, newsformat.Map{
			{Key: "required_application_version", Value: uint64(0)},
			{Key: "directories", Value: []any{}},
		})
	default:
		log.Printf("%s !! UNHANDLED bcat route %s%s", logPrefix, hostOf(r), p)
		http.Error(w, "no such bcat route", http.StatusNotFound)
	}
}
