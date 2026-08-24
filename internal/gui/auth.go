package gui

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/scrypt"
)

// Password and session handling for the whole GUI.
//
// Upstream has no login at all: the server holds kino.pub credentials, browses
// the mounted filesystem and starts multi-gigabyte downloads, so it was only
// ever meant for loopback (or, with -lan, a home network). Putting it on a
// public hostname needs an actual barrier, and this is it. The design is
// deliberately the same as the one already proven on the bluesky-feedgen admin
// page — the two projects are maintained together and a second design would be
// a second set of mistakes:
//
//   - the password is never stored, only a scrypt hash, and it is verified on
//     the request goroutine but with parameters small enough (~100 ms) not to
//     matter next to a download;
//   - sessions live server-side and the cookie carries nothing but a random
//     token, so there is no signature to forge and no payload to tamper with.
//     A restart signs everyone out, which is the right trade for a service that
//     restarts rarely;
//   - failed logins are rate limited per client *and* globally. The per-client
//     limit is the useful one behind the tunnel, where Cloudflare gives a real
//     address; the global one is what still holds if someone reaches the port
//     directly and forges that header.
//
// No dependency beyond golang.org/x/crypto, which the credential store already
// pulls in.

const (
	scryptN      = 16384 // ~16 MB of memory per hash at r=8
	scryptR      = 8
	scryptP      = 1
	scryptKeyLen = 64
	scryptSalt   = 16

	// scryptMaxMem bounds what a stored hash may ask us to allocate.
	//
	// x/crypto/scrypt has no maxmem parameter of its own: it checks that N is a
	// power of two and then allocates 128*r*N bytes. A hash carrying N=2^30
	// therefore asks for a terabyte and takes the whole process down with an
	// out-of-memory abort — from a LOGIN route, before it can return an error.
	// Node's crypto.scrypt has this cap built in, which is why the version this
	// was ported from did not need to say it out loud. Found by the test that
	// was written to prove the opposite.
	//
	// 64 MiB leaves room for N up to 65536 at r=8, four times what
	// HashPassword produces.
	scryptMaxMem = 64 << 20
)

// scryptParamsSane reports whether parameters read out of a stored hash can be
// honoured without risking the process. Checked before every scrypt.Key call.
func scryptParamsSane(n, r, p int) bool {
	if n <= 1 || r <= 0 || p <= 0 {
		return false
	}
	if n&(n-1) != 0 {
		return false // scrypt requires a power of two
	}
	if r > 1<<20 || p > 1<<20 {
		return false // keeps the multiplication below from overflowing
	}
	return n <= scryptMaxMem/(128*r)
}

// HashPassword turns a password into the string KINOPUB_AUTH_PASSWORD_HASH
// wants.
//
// Format: scrypt$N$r$p$<salt base64>$<key base64>. The parameters travel with
// the hash so raising them later does not invalidate existing passwords.
func HashPassword(password string) (string, error) {
	salt := make([]byte, scryptSalt)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key, err := scrypt.Key([]byte(password), salt, scryptN, scryptR, scryptP, scryptKeyLen)
	if err != nil {
		return "", err
	}
	return strings.Join([]string{
		"scrypt",
		strconv.Itoa(scryptN),
		strconv.Itoa(scryptR),
		strconv.Itoa(scryptP),
		base64.StdEncoding.EncodeToString(salt),
		base64.StdEncoding.EncodeToString(key),
	}, "$"), nil
}

// LooksLikeHash is checked at startup so a truncated paste fails at boot with a
// clear message, rather than silently becoming a password nobody can ever match.
//
// The salt and key lengths are checked EXACTLY, against what HashPassword
// produces: a hash cut short mid-line still splits into six plausible-looking
// parts and still base64-decodes, so "long enough" would wave it through.
// HashPassword is the only producer, which is what makes the exact check safe —
// but if its parameters are ever raised, this must be updated in the same
// commit or existing hashes stop being recognised at boot.
func LooksLikeHash(stored string) bool {
	parts := strings.Split(stored, "$")
	if len(parts) != 6 || parts[0] != "scrypt" {
		return false
	}
	n, err1 := strconv.Atoi(parts[1])
	r, err2 := strconv.Atoi(parts[2])
	p, err3 := strconv.Atoi(parts[3])
	if err1 != nil || err2 != nil || err3 != nil || !scryptParamsSane(n, r, p) {
		return false
	}
	salt, err := base64.StdEncoding.DecodeString(parts[4])
	if err != nil || len(salt) != scryptSalt {
		return false
	}
	key, err := base64.StdEncoding.DecodeString(parts[5])
	return err == nil && len(key) == scryptKeyLen
}

// VerifyPassword reports whether password produces stored. A malformed hash
// fails closed rather than erroring: every caller is on a login path where the
// only safe answer to "I cannot tell" is no.
func VerifyPassword(password, stored string) bool {
	parts := strings.Split(stored, "$")
	if len(parts) != 6 || parts[0] != "scrypt" {
		return false
	}
	n, err1 := strconv.Atoi(parts[1])
	r, err2 := strconv.Atoi(parts[2])
	p, err3 := strconv.Atoi(parts[3])
	// Before scrypt.Key, not after: absurd parameters must fail closed here,
	// because there is no "after" — the allocation aborts the process.
	if err1 != nil || err2 != nil || err3 != nil || !scryptParamsSane(n, r, p) {
		return false
	}
	salt, err := base64.StdEncoding.DecodeString(parts[4])
	if err != nil || len(salt) == 0 {
		return false
	}
	expected, err := base64.StdEncoding.DecodeString(parts[5])
	if err != nil || len(expected) == 0 {
		return false
	}
	actual, err := scrypt.Key([]byte(password), salt, n, r, p, len(expected))
	if err != nil {
		return false
	}
	return subtle.ConstantTimeCompare(actual, expected) == 1
}

// ---------------------------------------------------------------------------
// The gate
// ---------------------------------------------------------------------------

// Session lifetimes. Much longer than the feedgen admin page's 12 h / 1 h, and
// on purpose: that page is opened to change a setting and closed again, while
// this one is a phone's home screen shortcut for browsing a catalogue and
// watching a film. An hour of idle would mean signing in almost every time.
const (
	sessionTTL     = 30 * 24 * time.Hour
	sessionIdle    = 7 * 24 * time.Hour
	failureWindow  = 15 * time.Minute
	maxFailPerIP   = 5
	maxFailGlobal  = 20
	authCookieName = "kinopub_session"
	maxPasswordLen = 1024
)

type authSession struct {
	created  time.Time
	lastSeen time.Time
	ip       string
}

type failureCount struct {
	count int
	first time.Time
}

// authGate is the whole login system: what a valid password is, who is signed
// in, and who has been trying too hard. Nil means no login is configured, and
// every method tolerates that so callers need no branches.
type authGate struct {
	passwordHash string
	user         string

	// Read on every login, not captured once: enabling or disabling the second
	// factor from the UI must take effect without a restart.
	totp func() totpConfig

	mu           sync.Mutex
	sessions     map[string]*authSession
	failuresByIP map[string]*failureCount
	globalFail   failureCount
	// The last TOTP step accepted. A code is good once — see verifyTOTP.
	lastTOTPStep int64
	haveLastStep bool

	// A candidate secret held in memory until a code proves the authenticator
	// really has it. Writing it before that is how people lock themselves out
	// of a box they only mistyped into.
	pendingSecret string
	pendingAt     time.Time

	// Swapped in tests. Everything else about time here is real.
	now func() time.Time
}

func newAuthGate(passwordHash, user string, totp func() totpConfig) *authGate {
	if user == "" {
		user = "admin"
	}
	return &authGate{
		passwordHash: passwordHash,
		user:         user,
		totp:         totp,
		sessions:     map[string]*authSession{},
		failuresByIP: map[string]*failureCount{},
		now:          time.Now,
	}
}

// enabled reports whether a login is configured at all. A nil gate is the
// upstream behaviour: no accounts, loopback (or -lan) is the boundary.
func (g *authGate) enabled() bool { return g != nil && g.passwordHash != "" }

func (g *authGate) clock() time.Time {
	if g.now != nil {
		return g.now()
	}
	return time.Now()
}

// sweep drops expired sessions and stale failure counters. Called under mu.
func (g *authGate) sweep(now time.Time) {
	for token, s := range g.sessions {
		if now.Sub(s.created) > sessionTTL || now.Sub(s.lastSeen) > sessionIdle {
			delete(g.sessions, token)
		}
	}
	for ip, f := range g.failuresByIP {
		if now.Sub(f.first) > failureWindow {
			delete(g.failuresByIP, ip)
		}
	}
	if now.Sub(g.globalFail.first) > failureWindow {
		g.globalFail = failureCount{}
	}
}

func (g *authGate) lockedOut(ip string) bool {
	if f, ok := g.failuresByIP[ip]; ok && f.count >= maxFailPerIP {
		return true
	}
	return g.globalFail.count >= maxFailGlobal
}

func (g *authGate) recordFailure(ip string, now time.Time) {
	f, ok := g.failuresByIP[ip]
	if !ok {
		f = &failureCount{first: now}
		g.failuresByIP[ip] = f
	}
	f.count++
	if g.globalFail.count == 0 {
		g.globalFail.first = now
	}
	g.globalFail.count++
	log.Printf("auth: failed login from %s (%d in window, %d total)", ip, f.count, g.globalFail.count)
}

// sameUser compares the account name in constant time.
//
// Hashed before comparing so the comparison does not leak the expected length,
// which a raw comparison of unequal strings would.
func (g *authGate) sameUser(given string) bool {
	a := sha256.Sum256([]byte(given))
	b := sha256.Sum256([]byte(g.user))
	return hmac.Equal(a[:], b[:])
}

// sessionCount is what the Security panel reports.
func (g *authGate) sessionCount() int {
	if g == nil {
		return 0
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.sweep(g.clock())
	return len(g.sessions)
}

// validSession reports whether the request carries a live session, refreshing
// its idle deadline when it does.
func (g *authGate) validSession(r *http.Request) bool {
	c, err := r.Cookie(authCookieName)
	if err != nil || c.Value == "" {
		return false
	}
	now := g.clock()
	g.mu.Lock()
	defer g.mu.Unlock()
	g.sweep(now)
	s, ok := g.sessions[c.Value]
	if !ok {
		return false
	}
	s.lastSeen = now
	return true
}

func (g *authGate) dropSession(r *http.Request) {
	c, err := r.Cookie(authCookieName)
	if err != nil || c.Value == "" {
		return
	}
	g.mu.Lock()
	delete(g.sessions, c.Value)
	g.mu.Unlock()
}

// totpRequired reports whether the login form should ask for a code. Revealed
// unauthenticated, which costs nothing: one login attempt would tell you the
// same thing.
func (g *authGate) totpRequired() bool {
	if !g.enabled() || g.totp == nil {
		return false
	}
	c := g.totp()
	return c.Secret != "" || c.Broken
}

// clientIP is the address the rate limiter counts against.
//
// Cloudflare sets CF-Connecting-IP and it is the only address that means
// anything through the tunnel — every request arrives from the cloudflared
// container otherwise. It is trivially forged by anything reaching the port
// directly, which is exactly why the global limiter exists too.
func clientIP(r *http.Request) string {
	if cf := r.Header.Get("CF-Connecting-IP"); cf != "" && len(cf) < 64 {
		return cf
	}
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	if r.RemoteAddr != "" {
		return r.RemoteAddr
	}
	return "unknown"
}

// requestIsHTTPS reports whether the browser reached us over TLS.
//
// A cookie may only be marked Secure when it did: set it on a plain-HTTP LAN
// visit and the browser silently drops the cookie, which looks exactly like a
// broken login.
func requestIsHTTPS(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	return strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

func (g *authGate) setCookie(w http.ResponseWriter, r *http.Request, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     authCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   requestIsHTTPS(r),
		SameSite: http.SameSiteStrictMode,
		MaxAge:   int(sessionTTL / time.Second),
	})
}

func (g *authGate) clearCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     authCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   requestIsHTTPS(r),
		SameSite: http.SameSiteStrictMode,
		MaxAge:   -1,
	})
}

var errLoginRefused = errors.New("wrong username or password")

// login runs the whole check: rate limit, account name, password, then — and
// only then — the second factor. It returns the session token on success.
func (g *authGate) login(r *http.Request, user, password, code string) (string, error) {
	now := g.clock()
	ip := clientIP(r)

	g.mu.Lock()
	g.sweep(now)
	locked := g.lockedOut(ip)
	g.mu.Unlock()
	if locked {
		return "", errTooManyAttempts
	}

	// Verify even when a field is missing or malformed, so a wrong shape and a
	// wrong password cost the same time and reveal the same thing. The account
	// name is folded into the same verdict for the same reason: a wrong name
	// and a wrong password are indistinguishable from outside.
	ok := g.sameUser(user) &&
		password != "" &&
		len(password) <= maxPasswordLen &&
		VerifyPassword(password, g.passwordHash)

	if !ok {
		g.mu.Lock()
		g.recordFailure(ip, now)
		g.mu.Unlock()
		return "", errLoginRefused
	}

	// Only now, with the first factor proved, does the second one get asked
	// about — a wrong password must never reveal anything about the code.
	if g.totp != nil {
		cfg := g.totp()
		if cfg.Broken {
			log.Printf("auth: the TOTP secret is unusable — login refused, delete %s to turn 2FA off", totpStorePath())
			return "", errTOTPBroken
		}
		if cfg.Secret != "" {
			g.mu.Lock()
			var last *int64
			if g.haveLastStep {
				v := g.lastTOTPStep
				last = &v
			}
			g.mu.Unlock()

			verdict := verifyTOTP(cfg.Secret, code, now, last)
			if !verdict.OK {
				g.mu.Lock()
				g.recordFailure(ip, now)
				g.mu.Unlock()
				if verdict.Replay {
					log.Printf("auth: TOTP code reused from %s — refused", ip)
					return "", errTOTPReplay
				}
				if verdict.HasStep {
					log.Printf("auth: TOTP off by %d step(s) from %s", verdict.Drift, ip)
				}
				return "", errTOTPWrong
			}
			g.mu.Lock()
			g.lastTOTPStep = verdict.Step
			g.haveLastStep = true
			g.mu.Unlock()
		}
	}

	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		return "", fmt.Errorf("cannot create a session: %w", err)
	}
	token := base64.RawURLEncoding.EncodeToString(tokenBytes)

	g.mu.Lock()
	delete(g.failuresByIP, ip)
	g.sessions[token] = &authSession{created: now, lastSeen: now, ip: ip}
	g.mu.Unlock()

	log.Printf("auth: signed in from %s", ip)
	return token, nil
}

var (
	errTooManyAttempts = errors.New("too many attempts — try again later")
	errTOTPWrong       = errors.New("wrong or expired code")
	errTOTPReplay      = errors.New("that code has already been used — wait for the next one")
	errTOTPBroken      = errors.New("two-factor is configured but its secret cannot be read")
)

// ---------------------------------------------------------------------------
// The guard
// ---------------------------------------------------------------------------

// authExempt reports whether a path may be reached without a session.
//
// Only the SPA shell and the login routes: the page IS the login form, so it
// has to load before anyone can sign in. Everything under /api/ that is not a
// login route is closed, including the ones that merely read — /api/state names
// the kino.pub account, /api/fs walks the mounted filesystem, and /api/library
// lists what has been downloaded.
func authExempt(path string) bool {
	switch path {
	case "/api/auth/meta", "/api/auth/login", "/api/auth/logout":
		return true
	}
	return !strings.HasPrefix(path, "/api/")
}

// requireAuth wraps the mux so every request outside authExempt needs a live
// session. Without a configured password it is a no-op, which is upstream's
// behaviour.
func (s *Server) requireAuth(next http.Handler) http.Handler {
	if !s.auth.enabled() {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if authExempt(r.URL.Path) || s.auth.validSession(r) {
			next.ServeHTTP(w, r)
			return
		}
		// A browser asking for a page must get the page — it is the login form.
		// Only the API says 401, and the SPA turns that into the form.
		writeErr(w, http.StatusUnauthorized, "not signed in")
	})
}

// securityHeaders is applied to every response once a login exists, because
// then the app is on a public hostname.
//
// The bundle is self-hosted and the only remote thing the page touches is
// kino.pub, which is proxied through /api/img and /api/hls — so the policy can
// be 'self' throughout. blob: is what hls.js needs for MediaSource and its
// worker; without frame-ancestors the login form is clickjackable, and without
// form-action an injected form could still post the password somewhere else.
func securityHeaders(next http.Handler) http.Handler {
	const csp = "default-src 'self'; " +
		"script-src 'self' blob:; worker-src 'self' blob:; " +
		"style-src 'self' 'unsafe-inline'; " +
		"img-src 'self' data: blob:; " +
		"media-src 'self' blob:; " +
		"connect-src 'self' blob:; " +
		"font-src 'self' data:; " +
		"base-uri 'none'; form-action 'none'; frame-ancestors 'none'"
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", csp)
		h.Set("X-Frame-Options", "DENY")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}

// ---------------------------------------------------------------------------
// Routes
// ---------------------------------------------------------------------------

// handleAuthMeta says whether a login exists and whether it wants a code.
// Unauthenticated on purpose: one login attempt would reveal the same, and
// without it the form has to either ask for a code that is not wanted or hide
// one that is.
func (s *Server) handleAuthMeta(w http.ResponseWriter, r *http.Request) {
	resp := map[string]any{
		"required": s.auth.enabled(),
		"user":     "",
		"totp":     false,
		"signedIn": false,
	}
	if s.auth.enabled() {
		resp["user"] = s.auth.user
		resp["totp"] = s.auth.totpRequired()
		resp["signedIn"] = s.auth.validSession(r)
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleAuthLogin(w http.ResponseWriter, r *http.Request) {
	if !s.auth.enabled() {
		writeErr(w, http.StatusBadRequest, "no password is configured on this server")
		return
	}
	var body struct {
		User     string `json:"user"`
		Password string `json:"password"`
		TOTP     string `json:"totp"`
	}
	// A malformed body is not an error worth distinguishing: it cannot be a
	// correct password, so it takes the same path as a wrong one.
	_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&body)

	token, err := s.auth.login(r, body.User, body.Password, body.TOTP)
	if err != nil {
		switch {
		case errors.Is(err, errTooManyAttempts):
			w.Header().Set("Retry-After", strconv.Itoa(int(failureWindow/time.Second)))
			writeErr(w, http.StatusTooManyRequests, err.Error())
		case errors.Is(err, errTOTPBroken):
			writeErr(w, http.StatusUnauthorized,
				"two-factor is configured but its secret cannot be read. Delete "+
					totpStorePath()+" on the server to turn it off.")
		case errors.Is(err, errTOTPWrong), errors.Is(err, errTOTPReplay):
			writeJSON(w, http.StatusUnauthorized, map[string]any{
				"error": err.Error(), "needsTotp": true,
			})
		default:
			writeErr(w, http.StatusUnauthorized, err.Error())
		}
		return
	}
	s.auth.setCookie(w, r, token)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleAuthLogout(w http.ResponseWriter, r *http.Request) {
	if s.auth.enabled() {
		s.auth.dropSession(r)
		s.auth.clearCookie(w, r)
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
