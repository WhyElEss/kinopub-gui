package gui

import (
	"net/http"
	"strings"
	"time"
)

// Enrolling the second factor from the Security panel in Settings.
//
// Every route here is behind the guard: you must already be signed in to add a
// factor, which is what makes this safe to expose at all — unlike setting the
// FIRST factor, which would be an unprotected setup page.

// How long a started enrolment stays valid. Long enough to walk to a phone,
// short enough that an abandoned one does not sit in memory all day.
const totpPendingTTL = 10 * time.Minute

// totpIssuer labels the entry in the authenticator app. The hostname the page
// was reached on is the most useful label there is — an operator with several
// self-hosted things wants to see which one this is.
func totpIssuer(r *http.Request) string {
	host := r.Host
	if h, _, ok := strings.Cut(host, ":"); ok {
		host = h
	}
	if host == "" {
		return "kinopub-gui"
	}
	return host
}

// requireLogin refuses the enrolment routes when no password is configured.
//
// Without it a loopback install — which has no first factor at all — would
// offer to add a second one, and the Settings page would grow a panel that
// protects nothing. 404 rather than 403 so the UI can simply not render.
func (s *Server) requireLogin(w http.ResponseWriter) bool {
	if s.auth.enabled() {
		return true
	}
	writeErr(w, http.StatusNotFound, "no login is configured on this server")
	return false
}

func (s *Server) handleTOTPStatus(w http.ResponseWriter, r *http.Request) {
	if !s.requireLogin(w) {
		return
	}
	cfg := readTOTPConfig()
	writeJSON(w, http.StatusOK, map[string]any{
		"enabled": cfg.Secret != "" || cfg.Broken,
		"broken":  cfg.Broken,
		"source":  cfg.Source,
		// The environment wins and this page cannot edit it, so say so rather
		// than offering a disable button that would do nothing.
		"managedHere": cfg.Source != "env",
		"file":        totpStorePath(),
		"user":        s.auth.user,
		"sessions":    s.auth.sessionCount(),
	})
}

func (s *Server) handleTOTPBegin(w http.ResponseWriter, r *http.Request) {
	if !s.requireLogin(w) {
		return
	}
	cfg := readTOTPConfig()
	if cfg.Source == "env" {
		writeErr(w, http.StatusBadRequest,
			"two-factor is configured through KINOPUB_AUTH_TOTP_SECRET on this server. "+
				"Change it there — this page cannot edit the environment.")
		return
	}

	now := time.Now()
	s.auth.mu.Lock()
	// Idempotent while a setup is alive. Regenerating on every press means the
	// secret someone just saved in their password manager is already dead by
	// the time they come back to type a code.
	if s.auth.pendingSecret == "" || now.Sub(s.auth.pendingAt) > totpPendingTTL {
		secret, err := generateTOTPSecret()
		if err != nil {
			s.auth.mu.Unlock()
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		s.auth.pendingSecret = secret
		s.auth.pendingAt = now
	}
	secret, at := s.auth.pendingSecret, s.auth.pendingAt
	s.auth.mu.Unlock()

	uri := otpauthURI(secret, s.auth.user, totpIssuer(r))
	// The code the app should be showing right now, so a clock problem surfaces
	// here at setup rather than at the login screen a week later.
	expect, _ := totpCode(secret, now)
	writeJSON(w, http.StatusOK, map[string]any{
		"secret":       secret,
		"uri":          uri,
		"expectedNow":  expect,
		"expiresInSec": int((totpPendingTTL - now.Sub(at)) / time.Second),
	})
}

func (s *Server) handleTOTPEnable(w http.ResponseWriter, r *http.Request) {
	if !s.requireLogin(w) {
		return
	}
	var body struct {
		Code string `json:"code"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, "malformed request body")
		return
	}

	now := time.Now()
	s.auth.mu.Lock()
	secret, at := s.auth.pendingSecret, s.auth.pendingAt
	if secret == "" || now.Sub(at) > totpPendingTTL {
		s.auth.pendingSecret = ""
		s.auth.mu.Unlock()
		writeErr(w, http.StatusBadRequest, "that setup expired — start again")
		return
	}
	s.auth.mu.Unlock()

	if !verifyTOTP(secret, body.Code, now, nil).OK {
		writeErr(w, http.StatusBadRequest,
			"that code does not match. If the app shows a different number than expected, "+
				"this machine and your phone disagree about the time.")
		return
	}
	if err := storeTOTPSecret(secret, s.auth.user); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.auth.mu.Lock()
	s.auth.pendingSecret = ""
	s.auth.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "note": "Two-factor is on. It applies to the next sign-in.",
	})
}

// handleTOTPDisable asks for both factors again on purpose: a hijacked session
// must not be able to quietly remove the thing protecting the account.
func (s *Server) handleTOTPDisable(w http.ResponseWriter, r *http.Request) {
	if !s.requireLogin(w) {
		return
	}
	var body struct {
		Password string `json:"password"`
		Code     string `json:"code"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, "malformed request body")
		return
	}
	cfg := readTOTPConfig()
	if cfg.Source == "env" {
		writeErr(w, http.StatusBadRequest,
			"configured through KINOPUB_AUTH_TOTP_SECRET — remove it there and restart.")
		return
	}
	if cfg.Secret == "" && !cfg.Broken {
		writeErr(w, http.StatusBadRequest, "two-factor is not on")
		return
	}
	if !s.auth.enabled() {
		writeErr(w, http.StatusBadRequest, "no password is configured to check against")
		return
	}
	if body.Password == "" || len(body.Password) > maxPasswordLen ||
		!VerifyPassword(body.Password, s.auth.passwordHash) {
		writeErr(w, http.StatusUnauthorized, "wrong password")
		return
	}
	// A broken secret cannot be proved against, and demanding a code from it
	// would make the file impossible to remove from here — which is exactly
	// when you most need to.
	if cfg.Secret != "" && !verifyTOTP(cfg.Secret, body.Code, time.Now(), nil).OK {
		writeErr(w, http.StatusUnauthorized, "wrong or expired code")
		return
	}
	clearTOTPSecret()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
