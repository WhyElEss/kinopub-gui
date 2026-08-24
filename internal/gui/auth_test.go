package gui

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// A password used everywhere below, long enough to pass the CLI's own floor.
const testPassword = "correct horse battery staple"

func testHash(t *testing.T) string {
	t.Helper()
	h, err := HashPassword(testPassword)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	return h
}

func TestHashPasswordRoundTrips(t *testing.T) {
	h := testHash(t)
	if !LooksLikeHash(h) {
		t.Fatalf("LooksLikeHash rejected our own output: %q", h)
	}
	if !VerifyPassword(testPassword, h) {
		t.Error("the password does not verify against its own hash")
	}
	if VerifyPassword(testPassword+"x", h) {
		t.Error("a wrong password verified")
	}
	if VerifyPassword("", h) {
		t.Error("an empty password verified")
	}
}

func TestHashPasswordIsSalted(t *testing.T) {
	a, b := testHash(t), testHash(t)
	if a == b {
		t.Error("two hashes of the same password are identical — the salt is not random")
	}
}

// A truncated paste is the failure this guards: it still splits into six
// plausible parts and still base64-decodes, so only an exact length check
// catches it.
func TestLooksLikeHashRejectsMalformed(t *testing.T) {
	good := testHash(t)
	cases := map[string]string{
		"empty":                   "",
		"not scrypt":              strings.Replace(good, "scrypt", "bcrypt", 1),
		"too few parts":           "scrypt$16384$8$1$abc",
		"non-numeric N":           strings.Replace(good, "scrypt$16384", "scrypt$many", 1),
		"N of one":                strings.Replace(good, "$16384$", "$1$", 1),
		"truncated key":           good[:len(good)-8],
		"not base64":              good[:strings.LastIndex(good, "$")+1] + "!!!!",
		"short salt":              "scrypt$16384$8$1$YWJj$" + strings.Split(good, "$")[5],
		"N not a power of two":    strings.Replace(good, "$16384$", "$16385$", 1),
		"N beyond the memory cap": strings.Replace(good, "$16384$", "$1073741824$", 1),
	}
	for name, bad := range cases {
		if LooksLikeHash(bad) {
			t.Errorf("%s: LooksLikeHash accepted %q", name, bad)
		}
	}
}

// A hash carrying absurd parameters must fail closed.
//
// This is not theoretical politeness: x/crypto/scrypt allocates 128*r*N bytes
// after checking only that N is a power of two, so before scryptParamsSane
// existed this exact call took the whole process down with an out-of-memory
// abort — from a login route. The test was written expecting a false and got a
// crash.
func TestVerifyPasswordFailsClosedOnAbsurdParameters(t *testing.T) {
	if VerifyPassword(testPassword, "scrypt$1073741824$8$1$YWJjZGVmZ2hpamtsbW5vcA==$YWJj") {
		t.Error("a hash with an unusable N verified")
	}
	if VerifyPassword(testPassword, "not a hash at all") {
		t.Error("garbage verified")
	}
	if scryptParamsSane(1<<30, 8, 1) {
		t.Error("scryptParamsSane accepted a terabyte of work factor")
	}
	if !scryptParamsSane(scryptN, scryptR, scryptP) {
		t.Error("scryptParamsSane rejected the parameters HashPassword actually uses")
	}
}

// Base64 decoders ignore embedded newlines — Go's and Node's alike — so a hash
// that picked one up on its way through a file still decodes to the right bytes
// and still verifies. Recorded rather than "fixed": refusing to boot over a
// trailing newline would be a false alarm about a hash that works.
func TestATrailingNewlineIsHarmless(t *testing.T) {
	good := testHash(t)
	if !LooksLikeHash(good+"\n") || !VerifyPassword(testPassword, good+"\n") {
		t.Error("a trailing newline broke a hash that decodes to the same bytes")
	}
}

// ---------------------------------------------------------------------------
// The gate
// ---------------------------------------------------------------------------

func newTestGate(t *testing.T, totp func() totpConfig) *authGate {
	t.Helper()
	return newAuthGate(testHash(t), "", totp)
}

func loginReq() *http.Request {
	r := httptest.NewRequest("POST", "/api/auth/login", nil)
	r.RemoteAddr = "192.0.2.5:1234"
	return r
}

func TestLoginAcceptsAndIssuesASession(t *testing.T) {
	g := newTestGate(t, nil)
	token, err := g.login(loginReq(), "admin", testPassword, "")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if token == "" {
		t.Fatal("no session token")
	}
	if g.sessionCount() != 1 {
		t.Errorf("sessionCount = %d, want 1", g.sessionCount())
	}

	r := httptest.NewRequest("GET", "/api/state", nil)
	r.AddCookie(&http.Cookie{Name: authCookieName, Value: token})
	if !g.validSession(r) {
		t.Error("the token just issued does not open a session")
	}

	g.dropSession(r)
	if g.validSession(r) {
		t.Error("the session survived a logout")
	}
}

// A wrong name and a wrong password must be indistinguishable from outside:
// one account is the whole design, and a different answer for each would turn
// it into a way to confirm the name.
func TestLoginTellsNothingApartOnFailure(t *testing.T) {
	g := newTestGate(t, nil)
	_, errUser := g.login(loginReq(), "someone-else", testPassword, "")
	_, errPass := g.login(loginReq(), "admin", "wrong", "")
	if errUser == nil || errPass == nil {
		t.Fatal("a wrong name or a wrong password was accepted")
	}
	if errUser.Error() != errPass.Error() {
		t.Errorf("different answers: user=%q password=%q", errUser, errPass)
	}
}

func TestLoginRateLimitsPerClient(t *testing.T) {
	g := newTestGate(t, nil)
	for i := 0; i < maxFailPerIP; i++ {
		if _, err := g.login(loginReq(), "admin", "wrong", ""); err != errLoginRefused {
			t.Fatalf("attempt %d: err = %v, want a refusal", i, err)
		}
	}
	// The correct password no longer helps: the client is locked out for the
	// rest of the window.
	if _, err := g.login(loginReq(), "admin", testPassword, ""); err != errTooManyAttempts {
		t.Errorf("after %d failures: err = %v, want too-many-attempts", maxFailPerIP, err)
	}

	// Another client is unaffected while the global ceiling is far off.
	other := loginReq()
	other.RemoteAddr = "192.0.2.99:5000"
	if _, err := g.login(other, "admin", testPassword, ""); err != nil {
		t.Errorf("a different client was locked out too: %v", err)
	}
}

// The per-client limiter counts CF-Connecting-IP when it is there: behind the
// tunnel every request otherwise arrives from the same container address, and
// one attacker would lock out the whole internet.
func TestLoginCountsTheCloudflareAddress(t *testing.T) {
	g := newTestGate(t, nil)
	attacker := func() *http.Request {
		r := loginReq()
		r.Header.Set("CF-Connecting-IP", "203.0.113.7")
		return r
	}
	for i := 0; i < maxFailPerIP; i++ {
		g.login(attacker(), "admin", "wrong", "")
	}
	if _, err := g.login(attacker(), "admin", testPassword, ""); err != errTooManyAttempts {
		t.Errorf("the forwarded address was not counted: %v", err)
	}
	// The victim shares the tunnel's own address and must still get in.
	if _, err := g.login(loginReq(), "admin", testPassword, ""); err != nil {
		t.Errorf("a different forwarded address was locked out: %v", err)
	}
}

// The global limiter is what still holds when someone reaches the port directly
// and forges the header.
func TestLoginRateLimitsGloballyAcrossForgedAddresses(t *testing.T) {
	g := newTestGate(t, nil)
	for i := 0; i < maxFailGlobal; i++ {
		r := loginReq()
		r.Header.Set("CF-Connecting-IP", fmt.Sprintf("203.0.113.%d", i))
		g.login(r, "admin", "wrong", "")
	}
	fresh := loginReq()
	fresh.Header.Set("CF-Connecting-IP", "198.51.100.1")
	if _, err := g.login(fresh, "admin", testPassword, ""); err != errTooManyAttempts {
		t.Errorf("a never-seen address got through the global limit: %v", err)
	}
}

func TestSessionExpiresOnIdleAndOnAge(t *testing.T) {
	g := newTestGate(t, nil)
	now := time.Now()
	g.now = func() time.Time { return now }

	token, err := g.login(loginReq(), "admin", testPassword, "")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	r := httptest.NewRequest("GET", "/api/state", nil)
	r.AddCookie(&http.Cookie{Name: authCookieName, Value: token})

	// Used daily, it never trips the idle limit — which is the whole point of a
	// week: a phone that opens the app most days stays signed in.
	for i := 0; i < 25; i++ {
		now = now.Add(24 * time.Hour)
		if !g.validSession(r) {
			t.Fatalf("a session in daily use expired after %d days", i+1)
		}
	}
	// Left alone, it does not.
	now = now.Add(sessionIdle + time.Minute)
	if g.validSession(r) {
		t.Error("an idle session survived past the idle limit")
	}

	// And even in constant use it stops at the absolute lifetime.
	start := time.Now()
	now = start
	g.now = func() time.Time { return now }
	token, _ = g.login(loginReq(), "admin", testPassword, "")
	r2 := httptest.NewRequest("GET", "/api/state", nil)
	r2.AddCookie(&http.Cookie{Name: authCookieName, Value: token})
	for step := time.Duration(0); step < sessionTTL; step += 6 * time.Hour {
		now = start.Add(step)
		if !g.validSession(r2) {
			t.Fatalf("a session in constant use expired after %s", step)
		}
	}
	now = start.Add(sessionTTL + time.Hour)
	if g.validSession(r2) {
		t.Error("a session outlived its absolute lifetime")
	}
}

// ---------------------------------------------------------------------------
// The second factor, as the login sees it
// ---------------------------------------------------------------------------

func withTOTP(secret string) func() totpConfig {
	return func() totpConfig { return totpConfig{Secret: secret, Source: "file"} }
}

func TestLoginRequiresTheCodeWhenEnrolled(t *testing.T) {
	secret, err := generateTOTPSecret()
	if err != nil {
		t.Fatalf("generateTOTPSecret: %v", err)
	}
	g := newTestGate(t, withTOTP(secret))
	if !g.totpRequired() {
		t.Error("totpRequired() is false with a secret enrolled")
	}
	if _, err := g.login(loginReq(), "admin", testPassword, ""); err != errTOTPWrong {
		t.Errorf("no code: err = %v, want the code to be demanded", err)
	}
	code, _ := totpCode(secret, time.Now())
	if _, err := g.login(loginReq(), "admin", testPassword, code); err != nil {
		t.Errorf("the right code was refused: %v", err)
	}
}

// The code is checked strictly AFTER the password, so a wrong password reveals
// nothing about it — and must not burn the step either, or an attacker could
// invalidate the owner's own code by guessing passwords.
func TestWrongPasswordNeverReachesTheCode(t *testing.T) {
	secret, _ := generateTOTPSecret()
	g := newTestGate(t, withTOTP(secret))
	code, _ := totpCode(secret, time.Now())

	if _, err := g.login(loginReq(), "admin", "wrong", code); err != errLoginRefused {
		t.Fatalf("wrong password with a right code: err = %v, want the password refusal", err)
	}
	if g.haveLastStep {
		t.Error("a step was consumed by an attempt whose password was wrong")
	}
	// And the same code still works for its rightful owner.
	other := loginReq()
	other.RemoteAddr = "192.0.2.6:2222"
	if _, err := g.login(other, "admin", testPassword, code); err != nil {
		t.Errorf("the code was spent by the failed attempt: %v", err)
	}
}

func TestACodeWorksOnlyOnce(t *testing.T) {
	secret, _ := generateTOTPSecret()
	g := newTestGate(t, withTOTP(secret))
	code, _ := totpCode(secret, time.Now())

	if _, err := g.login(loginReq(), "admin", testPassword, code); err != nil {
		t.Fatalf("first use: %v", err)
	}
	second := loginReq()
	second.RemoteAddr = "192.0.2.7:3333"
	if _, err := g.login(second, "admin", testPassword, code); err != errTOTPReplay {
		t.Errorf("replay: err = %v, want the reuse refusal", err)
	}
}

// A secret that cannot be read refuses logins rather than dropping to one
// factor. Silently serving with less protection than was configured is the
// failure nobody notices.
func TestABrokenSecretRefusesLogins(t *testing.T) {
	g := newTestGate(t, func() totpConfig { return totpConfig{Source: "file", Broken: true} })
	if !g.totpRequired() {
		t.Error("a broken secret reads as two-factor off")
	}
	if _, err := g.login(loginReq(), "admin", testPassword, "000000"); err != errTOTPBroken {
		t.Errorf("err = %v, want the broken-secret refusal", err)
	}
}

// ---------------------------------------------------------------------------
// What the guard covers
// ---------------------------------------------------------------------------

// Everything under /api/ is closed, including the endpoints that only read: the
// snapshot names the kino.pub account, /api/fs walks the mounted filesystem and
// /api/library lists what has been downloaded.
func TestAuthExemptCoversOnlyThePageAndTheLoginRoutes(t *testing.T) {
	open := []string{"/", "/index.html", "/assets/index-abc.js", "/api/auth/meta",
		"/api/auth/login", "/api/auth/logout"}
	closed := []string{"/api/state", "/api/events", "/api/jobs", "/api/fs", "/api/library",
		"/api/settings", "/api/kp/login", "/api/hls", "/api/img", "/api/auth/totp",
		"/api/auth/totp/begin", "/api/auth/totp/disable"}
	for _, p := range open {
		if !authExempt(p) {
			t.Errorf("%s is closed but has to serve the login form", p)
		}
	}
	for _, p := range closed {
		if authExempt(p) {
			t.Errorf("%s is reachable without signing in", p)
		}
	}
}

func TestRequireAuthGuardsTheAPI(t *testing.T) {
	s := newTestServer(t)
	s.auth = newTestGate(t, nil)
	h := s.requireAuth(s.mux)

	r := httptest.NewRequest("GET", "/api/state", nil)
	r.Host = "127.0.0.1:8765"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("signed out: status = %d, want 401", w.Code)
	}

	token, err := s.auth.login(loginReq(), "admin", testPassword, "")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	r = httptest.NewRequest("GET", "/api/state", nil)
	r.Host = "127.0.0.1:8765"
	r.AddCookie(&http.Cookie{Name: authCookieName, Value: token})
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Errorf("signed in: status = %d, want 200", w.Code)
	}
}

// Without a configured password the guard is not installed at all, which is
// upstream's behaviour and what a loopback install should keep.
func TestRequireAuthIsANoOpWithoutAPassword(t *testing.T) {
	s := newTestServer(t)
	if s.auth.enabled() {
		t.Fatal("a test server picked up a password from the environment")
	}
	r := httptest.NewRequest("GET", "/api/state", nil)
	r.Host = "127.0.0.1:8765"
	w := httptest.NewRecorder()
	s.requireAuth(s.mux).ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
}

// The login and logout routes must sit in front of the guard, or signing in
// would require being signed in.
func TestLoginRouteIsReachableWhileSignedOut(t *testing.T) {
	s := newTestServer(t)
	s.auth = newTestGate(t, nil)
	h := s.requireAuth(s.mux)

	body := strings.NewReader(`{"user":"admin","password":"` + testPassword + `"}`)
	r := httptest.NewRequest("POST", "/api/auth/login", body)
	r.Host = "127.0.0.1:8765"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d (%s), want 200", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Header().Get("Set-Cookie"), authCookieName+"=") {
		t.Errorf("no session cookie: %q", w.Header().Get("Set-Cookie"))
	}
	for _, want := range []string{"HttpOnly", "SameSite=Strict", "Path=/"} {
		if !strings.Contains(w.Header().Get("Set-Cookie"), want) {
			t.Errorf("cookie is missing %s: %q", want, w.Header().Get("Set-Cookie"))
		}
	}
	// Secure is left OFF over plain HTTP: set it on a LAN visit and the browser
	// silently drops the cookie, which looks exactly like a broken login.
	if strings.Contains(w.Header().Get("Set-Cookie"), "Secure") {
		t.Errorf("Secure on a plain-HTTP request: %q", w.Header().Get("Set-Cookie"))
	}
}

func TestLoginCookieIsSecureBehindTheTunnel(t *testing.T) {
	s := newTestServer(t)
	s.auth = newTestGate(t, nil)

	body := strings.NewReader(`{"user":"admin","password":"` + testPassword + `"}`)
	r := httptest.NewRequest("POST", "/api/auth/login", body)
	r.Host = "kino.example.com"
	r.Header.Set("X-Forwarded-Proto", "https")
	w := httptest.NewRecorder()
	s.handleAuthLogin(w, r)
	if !strings.Contains(w.Header().Get("Set-Cookie"), "Secure") {
		t.Errorf("no Secure over https: %q", w.Header().Get("Set-Cookie"))
	}
}

func TestAuthMetaSaysWhatTheFormNeeds(t *testing.T) {
	s := newTestServer(t)
	secret, _ := generateTOTPSecret()
	s.auth = newTestGate(t, withTOTP(secret))

	w := httptest.NewRecorder()
	s.handleAuthMeta(w, httptest.NewRequest("GET", "/api/auth/meta", nil))
	var got struct {
		Required bool   `json:"required"`
		User     string `json:"user"`
		TOTP     bool   `json:"totp"`
		SignedIn bool   `json:"signedIn"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !got.Required || !got.TOTP || got.SignedIn || got.User != "admin" {
		t.Errorf("meta = %+v", got)
	}
}

// ---------------------------------------------------------------------------
// The public hostname
// ---------------------------------------------------------------------------

// -public-host is the ONLY way a name public DNS can resolve gets past the
// anti-rebinding check, and it must let exactly that one name through.
func TestPublicHostAllowsOnlyItself(t *testing.T) {
	const pub = "kino.stanislavski.me"
	cases := []struct {
		host string
		want bool
	}{
		{pub, true},
		{pub + ":443", true},
		{strings.ToUpper(pub), true},
		{"evil.example.com", false},
		{"kino.stanislavski.me.evil.example.com", false},
		{"stanislavski.me", false},
		{"127.0.0.1:8765", true}, // loopback, always
	}
	for _, c := range cases {
		if got := hostAllowed(c.host, false, pub); got != c.want {
			t.Errorf("hostAllowed(%q, lan=false, public=%q) = %v, want %v", c.host, pub, got, c.want)
		}
	}
	// And without the flag it is refused like any other public name, even with
	// -lan on.
	if hostAllowed(pub, true, "") {
		t.Error("a public host got through with no -public-host set")
	}
}

func TestPublicOriginIsAcceptedForItsOwnHost(t *testing.T) {
	const pub = "kino.stanislavski.me"
	if !originAllowed("https://"+pub, pub, false, pub) {
		t.Error("the page's own origin was refused")
	}
	if originAllowed("https://evil.example.com", pub, false, pub) {
		t.Error("a foreign origin was accepted")
	}
}

func TestSecurityHeadersAreSet(t *testing.T) {
	h := securityHeaders(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
	for _, k := range []string{"Content-Security-Policy", "X-Frame-Options",
		"X-Content-Type-Options", "Referrer-Policy"} {
		if w.Header().Get(k) == "" {
			t.Errorf("%s is not set", k)
		}
	}
	csp := w.Header().Get("Content-Security-Policy")
	// frame-ancestors is what stops the login form being clickjacked, and
	// form-action is what stops an injected form posting the password away.
	for _, want := range []string{"frame-ancestors 'none'", "form-action 'none'", "base-uri 'none'"} {
		if !strings.Contains(csp, want) {
			t.Errorf("CSP is missing %q: %s", want, csp)
		}
	}
	// The player needs these; a policy that forgets them breaks playback in a
	// way that only shows up on the public hostname.
	for _, want := range []string{"media-src 'self' blob:", "worker-src 'self' blob:"} {
		if !strings.Contains(csp, want) {
			t.Errorf("CSP would break the player, missing %q: %s", want, csp)
		}
	}
}
