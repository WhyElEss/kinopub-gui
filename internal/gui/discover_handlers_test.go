package gui

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// All discovery handlers resolve the kino.pub client first; with no stored
// credentials they must reply 401 (not signed in), never panic.
func TestDiscoverHandlers_Unauthenticated(t *testing.T) {
	s := newTestServer(t)
	handlers := map[string]http.HandlerFunc{
		"/api/discover/search?q=x":   s.handleDiscoverSearch,
		"/api/discover/items":        s.handleDiscoverItems,
		"/api/discover/collections":  s.handleDiscoverCollections,
		"/api/discover/countries":    s.handleDiscoverCountries,
		"/api/discover/history":      s.handleDiscoverHistory,
		"/api/discover/watching":     s.handleDiscoverWatching,
		"/api/discover/genres":       s.handleDiscoverGenres,
		"/api/discover/bookmarks":    s.handleDiscoverBookmarks,
		"/api/discover/item?id=1":    s.handleDiscoverItem,
		"/api/discover/similar?id=1": s.handleDiscoverSimilar,
		"/api/kp/user":               s.handleKPUser,
	}
	for path, h := range handlers {
		req := httptest.NewRequest("GET", path, nil)
		w := httptest.NewRecorder()
		h(w, req)
		if w.Code != http.StatusUnauthorized {
			t.Errorf("%s: status = %d, want 401", path, w.Code)
		}
	}
}

func TestDiscoverSearch_RequiresQuery(t *testing.T) {
	s := newTestServer(t)
	// Without "q" it would short-circuit on the missing query — but the client is
	// resolved first, so an unauthenticated server returns 401. Confirm a signed-out
	// search never panics and returns an error status.
	req := httptest.NewRequest("GET", "/api/discover/search", nil)
	w := httptest.NewRecorder()
	s.handleDiscoverSearch(w, req)
	if w.Code != http.StatusUnauthorized && w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 401 or 400", w.Code)
	}
}

func TestDiscoverItemHandlers_RequireID(t *testing.T) {
	// item/similar/collection/bookmark require an id; but client resolution comes
	// first. To exercise the id-required branch we stub a client by signing in is
	// not feasible here, so we only assert the unauthenticated path is handled
	// (covered above). This test documents that id-less requests do not 5xx.
	s := newTestServer(t)
	for _, path := range []string{"/api/discover/collection", "/api/discover/bookmark"} {
		req := httptest.NewRequest("GET", path, nil)
		w := httptest.NewRecorder()
		switch path {
		case "/api/discover/collection":
			s.handleDiscoverCollection(w, req)
		case "/api/discover/bookmark":
			s.handleDiscoverBookmark(w, req)
		}
		if w.Code >= 500 {
			t.Errorf("%s should not 5xx, got %d", path, w.Code)
		}
	}
}

func TestProxyImage_RejectsBadURL(t *testing.T) {
	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/img", nil)
	proxyImage(w, req, "not a url with no scheme")
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestProxyImage_RejectsNonHTTPScheme(t *testing.T) {
	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/img", nil)
	proxyImage(w, req, "ftp://host/image.jpg")
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestHandleImage_EmptyURL(t *testing.T) {
	s := newTestServer(t)
	req := httptest.NewRequest("GET", "/api/img", nil)
	w := httptest.NewRecorder()
	s.handleImage(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("empty image url: status = %d, want 400", w.Code)
	}
}

func TestOriginAllowed(t *testing.T) {
	cases := []struct {
		origin, host string
		allowLAN     bool
		want         bool
	}{
		{"http://127.0.0.1:8765", "127.0.0.1:8765", false, true}, // exact match
		{"http://localhost:8765", "127.0.0.1:8765", false, true}, // loopback ↔ loopback
		{"http://[::1]:8765", "127.0.0.1:8765", false, true},
		{"http://evil.example.com", "127.0.0.1:8765", false, false},
		{"not a url", "127.0.0.1:8765", false, false},
		{"http://", "127.0.0.1:8765", false, false}, // empty host
		// The page served over the LAN address posts back to it: same Origin and
		// Host, so it passes even with LAN access off.
		{"http://192.168.2.200:8765", "192.168.2.200:8765", false, true},
		// A LAN origin against a different host is only allowed with -lan.
		{"http://192.168.2.200:8765", "127.0.0.1:8765", false, false},
		{"http://192.168.2.200:8765", "127.0.0.1:8765", true, true},
		{"http://evil.example.com", "192.168.2.200:8765", true, false},
	}
	for _, c := range cases {
		if got := originAllowed(c.origin, c.host, c.allowLAN, ""); got != c.want {
			t.Errorf("originAllowed(%q, %q, lan=%v) = %v, want %v", c.origin, c.host, c.allowLAN, got, c.want)
		}
	}
}

func TestIsPrivateHost(t *testing.T) {
	cases := map[string]bool{
		"192.168.2.200:8765": true,
		"10.0.0.5":           true,
		"172.16.3.9:8765":    true,
		"[fd00::1]:8765":     true,
		"nas-pi.local:8765":  true,
		"nas-pi":             true,
		"8.8.8.8:8765":       false,
		"evil.example.com":   false,
		"":                   false,
	}
	for host, want := range cases {
		if got := isPrivateHost(host); got != want {
			t.Errorf("isPrivateHost(%q) = %v, want %v", host, got, want)
		}
	}
}

func TestGuardLocalOnly_RejectsNonLoopbackHost(t *testing.T) {
	called := false
	guard := guardLocalOnly(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }), false, "")
	req := httptest.NewRequest("GET", "/api/health", nil)
	req.Host = "evil.example.com"
	w := httptest.NewRecorder()
	guard.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden || called {
		t.Errorf("forged host: status=%d called=%v, want 403 and not called", w.Code, called)
	}
}

func TestGuardLocalOnly_RejectsLANHostWithoutFlag(t *testing.T) {
	called := false
	guard := guardLocalOnly(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }), false, "")
	req := httptest.NewRequest("GET", "/api/health", nil)
	req.Host = "192.168.2.200:8765"
	w := httptest.NewRecorder()
	guard.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden || called {
		t.Errorf("LAN host without -lan: status=%d called=%v, want 403 and not called", w.Code, called)
	}
}

func TestGuardLocalOnly_AllowsLANHostWithFlag(t *testing.T) {
	called := false
	guard := guardLocalOnly(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}), true, "")
	req := httptest.NewRequest("POST", "/api/jobs", nil)
	req.Host = "192.168.2.200:8765"
	req.Header.Set("Origin", "http://192.168.2.200:8765")
	w := httptest.NewRecorder()
	guard.ServeHTTP(w, req)
	if w.Code != http.StatusOK || !called {
		t.Errorf("LAN host with -lan: status=%d called=%v, want 200", w.Code, called)
	}
}

func TestGuardLocalOnly_RejectsPublicHostEvenWithLAN(t *testing.T) {
	called := false
	guard := guardLocalOnly(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }), true, "")
	req := httptest.NewRequest("GET", "/api/health", nil)
	req.Host = "kino.example.com"
	w := httptest.NewRecorder()
	guard.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden || called {
		t.Errorf("public host with -lan: status=%d called=%v, want 403", w.Code, called)
	}
}

func TestGuardLocalOnly_AllowsLoopback(t *testing.T) {
	called := false
	guard := guardLocalOnly(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}), false, "")
	req := httptest.NewRequest("GET", "/api/health", nil)
	req.Host = "127.0.0.1:8765"
	w := httptest.NewRecorder()
	guard.ServeHTTP(w, req)
	if w.Code != http.StatusOK || !called {
		t.Errorf("loopback should pass: status=%d called=%v", w.Code, called)
	}
}
