// SPDX-License-Identifier: AGPL-3.0-only
package newsexperiment

import (
	"net/http"
	"sync/atomic"
)

// Switch serves whichever fixture handler was stored last. Handler loads a
// whole tree into memory up front, so a switch is one pointer swap:
// requests already running finish on the tree they started with, and the
// old directory can be deleted afterwards.
type Switch struct{ h atomic.Pointer[http.Handler] }

// Store makes h the served handler.
func (s *Switch) Store(h http.Handler) { s.h.Store(&h) }

func (s *Switch) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h := s.h.Load()
	if h == nil {
		http.Error(w, "no fixture tree loaded", http.StatusServiceUnavailable)
		return
	}
	(*h).ServeHTTP(w, r)
}
