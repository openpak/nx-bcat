package main

// nx-baas's helper names, bodies in nx-common (as nx-pctl's common.go).

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/openpak/nx-common/nxhttp"
)

const logPrefix = "[nx-bcat]"

func getenv(k, d string) string                          { return nxhttp.Getenv(k, d) }
func writeJSON(w http.ResponseWriter, status int, v any) { nxhttp.WriteJSON(w, status, v) }
func hostOf(r *http.Request) string                      { return nxhttp.HostOf(r) }
func itoa(n int64) string                                { return strconv.FormatInt(n, 10) }

func apiErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]any{"error": msg})
}

func jsonNumber(s string) json.Number { return json.Number(s) }
