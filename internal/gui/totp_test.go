package gui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The published RFC 6238 vectors. An implementation that is merely
// self-consistent passes anything you write against itself, so these are the
// only test here that proves the algorithm is TOTP and not something that
// merely behaves like it.
//
// The RFC prints eight digits; six is what an authenticator app shows and what
// this server asks for, so these are the last six of each published value.
func TestTOTPMatchesTheRFCVectors(t *testing.T) {
	// "12345678901234567890" in base32 — the RFC's SHA-1 seed.
	const secret = "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ"
	cases := []struct {
		unix int64
		want string
	}{
		{59, "287082"},
		{1111111109, "081804"},
		{1111111111, "050471"},
		{1234567890, "005924"},
		{2000000000, "279037"},
		{20000000000, "353130"},
	}
	for _, c := range cases {
		got, err := totpCode(secret, time.Unix(c.unix, 0))
		if err != nil {
			t.Fatalf("T=%d: %v", c.unix, err)
		}
		if got != c.want {
			t.Errorf("T=%d: code = %s, want %s", c.unix, got, c.want)
		}
	}
}

func TestBase32RoundTrips(t *testing.T) {
	if got := base32Encode([]byte("12345678901234567890")); got != "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ" {
		t.Errorf("base32Encode = %s", got)
	}
	back, err := base32Decode("GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ")
	if err != nil || string(back) != "12345678901234567890" {
		t.Errorf("base32Decode = %q, %v", back, err)
	}
	// An authenticator app shows the secret in spaced groups, and people paste
	// it back that way.
	spaced, err := base32Decode("gezd gnbv gy3t qojq-GEZDGNBVGY3TQOJQ")
	if err != nil || string(spaced) != "12345678901234567890" {
		t.Errorf("spaced/lowercase secret: %q, %v", spaced, err)
	}
	if _, err := base32Decode("not-valid-base32!"); err == nil {
		t.Error("garbage decoded")
	}
	if _, err := base32Decode(""); err == nil {
		t.Error("an empty secret decoded")
	}
}

// One step either side and no more. A wider window is how TOTP quietly stops
// being a second factor.
func TestVerifyTOTPWindowIsOneStep(t *testing.T) {
	secret, _ := generateTOTPSecret()
	now := time.Unix(1700000000, 0)
	for _, d := range []int{-2, -1, 0, 1, 2} {
		code, _ := totpCode(secret, now.Add(time.Duration(d)*totpStepSeconds*time.Second))
		got := verifyTOTP(secret, code, now, nil).OK
		want := d >= -totpWindow && d <= totpWindow
		if got != want {
			t.Errorf("drift %d step(s): accepted = %v, want %v", d, got, want)
		}
	}
}

func TestVerifyTOTPRefusesAUsedStep(t *testing.T) {
	secret, _ := generateTOTPSecret()
	now := time.Unix(1700000000, 0)
	code, _ := totpCode(secret, now)

	first := verifyTOTP(secret, code, now, nil)
	if !first.OK {
		t.Fatal("the first use was refused")
	}
	again := verifyTOTP(secret, code, now, &first.Step)
	if again.OK || !again.Replay {
		t.Errorf("second use: %+v, want a replay refusal", again)
	}
	// An OLDER step is refused too, not just the same one: a code captured a
	// window ago must not become usable again.
	older, _ := totpCode(secret, now.Add(-totpStepSeconds*time.Second))
	if verifyTOTP(secret, older, now, &first.Step).OK {
		t.Error("a code from a step before the last used one was accepted")
	}
}

func TestVerifyTOTPRejectsMalformedCodes(t *testing.T) {
	secret, _ := generateTOTPSecret()
	now := time.Now()
	for _, bad := range []string{"", "12345", "1234567", "abcdef", "12 34 56", "12345a"} {
		if verifyTOTP(secret, bad, now, nil).OK {
			t.Errorf("%q was accepted", bad)
		}
	}
	// Whitespace around a real code is fine — people paste it with a space.
	code, _ := totpCode(secret, now)
	if !verifyTOTP(secret, " "+code+"\n", now, nil).OK {
		t.Error("a padded but correct code was refused")
	}
}

func TestLooksLikeSecretFloor(t *testing.T) {
	got, err := generateTOTPSecret()
	if err != nil {
		t.Fatalf("generateTOTPSecret: %v", err)
	}
	if !looksLikeSecret(got) {
		t.Errorf("our own secret was rejected: %s", got)
	}
	// Under RFC 4226's 16-byte floor.
	if looksLikeSecret("GEZDGNBVGY3TQOJQ") { // 10 bytes
		t.Error("a 10-byte secret passed the floor")
	}
	if looksLikeSecret("nonsense!") {
		t.Error("garbage passed the floor")
	}
}

func TestOtpauthURICarriesTheParameters(t *testing.T) {
	uri := otpauthURI("GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ", "admin", "kino.example.com")
	for _, want := range []string{
		"otpauth://totp/", "secret=GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ",
		"issuer=kino.example.com", "algorithm=SHA1", "digits=6", "period=30",
	} {
		if !strings.Contains(uri, want) {
			t.Errorf("URI is missing %q: %s", want, uri)
		}
	}
}

// ---------------------------------------------------------------------------
// The store
// ---------------------------------------------------------------------------

func TestTOTPStoreRoundTrips(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	if got := readTOTPConfig(); got.Secret != "" || got.Broken || got.Source != "" {
		t.Fatalf("a fresh config dir reads as %+v, want empty", got)
	}

	secret, _ := generateTOTPSecret()
	if err := storeTOTPSecret(secret, "admin"); err != nil {
		t.Fatalf("storeTOTPSecret: %v", err)
	}
	got := readTOTPConfig()
	if got.Secret != secret || got.Source != "file" || got.Broken {
		t.Errorf("read back %+v, want the stored secret from a file", got)
	}

	// 0600: the file is a second factor, and a world-readable one is not one.
	info, err := os.Stat(totpStorePath())
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("mode = %o, want 600", perm)
	}

	if !clearTOTPSecret() {
		t.Error("clearTOTPSecret reported failure")
	}
	if readTOTPConfig().Secret != "" {
		t.Error("the secret survived being cleared")
	}
}

// A file that has gone bad reads as ENABLED-but-broken, never as off. Dropping
// silently to one factor is the failure nobody notices, and the login refuses
// rather than doing that (see TestABrokenSecretRefusesLogins).
func TestAnUnreadableSecretReadsAsBroken(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	path := filepath.Join(dir, "kinopub", "totp.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{
		"not json":     "{{{",
		"no secret":    `{"account":"admin"}`,
		"short secret": `{"secret":"GEZDGNBVGY3TQOJQ"}`,
		"not base32":   `{"secret":"!!!!!!!!!!!!!!!!!!!!!!!!"}`,
	} {
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		got := readTOTPConfig()
		if !got.Broken || got.Secret != "" {
			t.Errorf("%s: read as %+v, want broken with no secret", name, got)
		}
	}
}

// The environment wins, and the page says so rather than offering a disable
// button it could not honour.
func TestTheEnvironmentBeatsTheFile(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	fileSecret, _ := generateTOTPSecret()
	if err := storeTOTPSecret(fileSecret, "admin"); err != nil {
		t.Fatal(err)
	}
	envSecret, _ := generateTOTPSecret()
	t.Setenv("KINOPUB_AUTH_TOTP_SECRET", envSecret)

	got := readTOTPConfig()
	if got.Secret != envSecret || got.Source != "env" {
		t.Errorf("read %+v, want the environment's secret", got)
	}

	// And a malformed one there is broken, not ignored.
	t.Setenv("KINOPUB_AUTH_TOTP_SECRET", "nope")
	if got := readTOTPConfig(); !got.Broken || got.Source != "env" {
		t.Errorf("malformed env secret read as %+v, want broken", got)
	}
}
