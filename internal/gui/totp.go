package gui

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// TOTP (RFC 6238) for the login — the same thing an authenticator app does, in
// about a hundred lines and with no dependency: HMAC-SHA1 and a base32 decoder
// are all it takes, and the standard library has the first one.
//
// What it buys: a stolen password stops being enough. This server sits on a
// public hostname, holds a kino.pub session and can fill a disk, so that is
// worth having.
//
// What it costs, and the reason it is optional: TOTP needs the server clock to
// be roughly right. The escape hatch is good — delete the secret file, no
// restart needed — but a lockout is still possible from a machine you cannot
// reach.

const (
	totpStepSeconds = 30
	totpDigits      = 6
	// One step either side: ±30 s of clock skew between server and phone.
	// Wider windows are how TOTP quietly stops being a second factor.
	totpWindow = 1
)

const base32Alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567"

var notBase32 = regexp.MustCompile(`[^A-Z2-7]`)

func base32Decode(input string) ([]byte, error) {
	clean := strings.ToUpper(input)
	clean = strings.NewReplacer(" ", "", "\t", "", "\n", "", "\r", "", "-", "").Replace(clean)
	clean = strings.TrimRight(clean, "=")
	if clean == "" || notBase32.MatchString(clean) {
		return nil, errors.New("not a base32 secret (A–Z and 2–7 only)")
	}
	var (
		out   []byte
		bits  uint
		value uint32
	)
	for _, ch := range clean {
		value = value<<5 | uint32(strings.IndexRune(base32Alphabet, ch))
		bits += 5
		if bits >= 8 {
			out = append(out, byte(value>>(bits-8)))
			bits -= 8
		}
	}
	return out, nil
}

func base32Encode(buf []byte) string {
	var (
		sb    strings.Builder
		bits  uint
		value uint32
	)
	for _, b := range buf {
		value = value<<8 | uint32(b)
		bits += 8
		for bits >= 5 {
			sb.WriteByte(base32Alphabet[(value>>(bits-5))&31])
			bits -= 5
		}
	}
	if bits > 0 {
		sb.WriteByte(base32Alphabet[(value<<(5-bits))&31])
	}
	return sb.String()
}

func hotp(key []byte, counter uint64) string {
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], counter)
	mac := hmac.New(sha1.New, key)
	mac.Write(buf[:])
	sum := mac.Sum(nil)
	offset := sum[len(sum)-1] & 0x0f
	code := (uint32(sum[offset]&0x7f) << 24) |
		(uint32(sum[offset+1]) << 16) |
		(uint32(sum[offset+2]) << 8) |
		uint32(sum[offset+3])
	mod := uint32(1)
	for i := 0; i < totpDigits; i++ {
		mod *= 10
	}
	return fmt.Sprintf("%0*d", totpDigits, code%mod)
}

func totpStep(at time.Time) int64 { return at.Unix() / totpStepSeconds }

// totpCode is what an authenticator app should be showing at that instant.
// Used by the enrolment self-check and by the tests.
func totpCode(secret string, at time.Time) (string, error) {
	key, err := base32Decode(secret)
	if err != nil {
		return "", err
	}
	return hotp(key, uint64(totpStep(at))), nil
}

type totpVerdict struct {
	OK bool
	// Step is meaningful only when HasStep is set.
	Step    int64
	HasStep bool
	// Drift is how far off the accepted step was, in steps. Non-zero on a slow
	// clock; logged server-side because a lockout caused by drift is otherwise
	// a complete mystery. Never returned to the client.
	Drift int
	// Replay is set when the code was correct for a step already used.
	Replay bool
}

// verifyTOTP checks a code, refusing one that has already been used.
//
// A code is valid once. Without that, a code seen over someone's shoulder — or
// sitting in a proxy log — works for the rest of its 30-second window, which is
// exactly the reuse a second factor is supposed to prevent.
func verifyTOTP(secret, token string, at time.Time, lastUsedStep *int64) totpVerdict {
	clean := strings.Map(func(r rune) rune {
		if r == ' ' || r == '\t' || r == '\n' || r == '\r' {
			return -1
		}
		return r
	}, token)
	if len(clean) != totpDigits {
		return totpVerdict{}
	}
	for _, r := range clean {
		if r < '0' || r > '9' {
			return totpVerdict{}
		}
	}
	key, err := base32Decode(secret)
	if err != nil {
		return totpVerdict{}
	}
	now := totpStep(at)
	for d := -totpWindow; d <= totpWindow; d++ {
		step := now + int64(d)
		if hmac.Equal([]byte(hotp(key, uint64(step))), []byte(clean)) {
			if lastUsedStep != nil && step <= *lastUsedStep {
				return totpVerdict{Step: step, HasStep: true, Drift: d, Replay: true}
			}
			return totpVerdict{OK: true, Step: step, HasStep: true, Drift: d}
		}
	}
	return totpVerdict{}
}

func generateTOTPSecret() (string, error) {
	buf := make([]byte, 20)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base32Encode(buf), nil
}

// otpauthURI is what an authenticator app scans, or accepts pasted. issuer and
// account are only labels — they decide what the entry is called in the app,
// nothing else.
func otpauthURI(secret, account, issuer string) string {
	q := url.Values{}
	q.Set("secret", secret)
	q.Set("issuer", issuer)
	q.Set("algorithm", "SHA1")
	q.Set("digits", fmt.Sprint(totpDigits))
	q.Set("period", fmt.Sprint(totpStepSeconds))
	return "otpauth://totp/" + url.PathEscape(issuer+":"+account) + "?" + q.Encode()
}

// looksLikeSecret is the startup check for KINOPUB_AUTH_TOTP_SECRET.
func looksLikeSecret(secret string) bool {
	// 16 bytes is the floor RFC 4226 recommends; 20 is what generateTOTPSecret
	// makes.
	key, err := base32Decode(secret)
	return err == nil && len(key) >= 16
}

// ---------------------------------------------------------------------------
// Where the secret lives
// ---------------------------------------------------------------------------
//
// NOT in the environment, unlike the password hash, and the difference is not
// an oversight. The password must exist BEFORE the server is public: if it were
// set through the UI, a fresh install would offer an unprotected setup page on
// a public hostname and whoever found it first would own the box. A second
// factor has no such window — only someone already signed in can add one — so
// it can be a button, and therefore has to live somewhere the container can
// actually write. /config is a mounted volume; the environment is not writable
// at all.
//
// Read per request rather than at startup, so enabling or disabling takes
// effect immediately instead of after a restart.

func totpStorePath() string {
	dir, err := configDir()
	if err != nil {
		return "totp.json"
	}
	return filepath.Join(dir, "totp.json")
}

type totpConfig struct {
	Secret string
	// Source is "env", "file" or "".
	Source string
	// Broken means the file exists but cannot be used. Treated as
	// ENABLED-but-broken rather than as off: silently dropping to one factor
	// because a file got corrupted is the failure nobody notices. The way out
	// is deleting the file, which is the documented escape hatch anyway.
	Broken bool
}

func readTOTPConfig() totpConfig {
	// An operator who set it in the environment meant it, and the UI cannot
	// unset it there — so the environment wins and the page says where it came
	// from.
	if fromEnv := os.Getenv("KINOPUB_AUTH_TOTP_SECRET"); fromEnv != "" {
		if looksLikeSecret(fromEnv) {
			return totpConfig{Secret: fromEnv, Source: "env"}
		}
		return totpConfig{Source: "env", Broken: true}
	}
	raw, err := os.ReadFile(totpStorePath())
	if err != nil {
		return totpConfig{}
	}
	var parsed struct {
		Secret string `json:"secret"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil || !looksLikeSecret(parsed.Secret) {
		return totpConfig{Source: "file", Broken: true}
	}
	return totpConfig{Secret: parsed.Secret, Source: "file"}
}

func storeTOTPSecret(secret, account string) error {
	if !looksLikeSecret(secret) {
		return errors.New("refusing to store an unusable secret")
	}
	file := totpStorePath()
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return err
	}
	body, err := json.MarshalIndent(map[string]string{
		"secret":    secret,
		"account":   account,
		"enabledAt": time.Now().UTC().Format(time.RFC3339),
	}, "", "  ")
	if err != nil {
		return err
	}
	tmp := fmt.Sprintf("%s.tmp-%d", file, os.Getpid())
	if err := os.WriteFile(tmp, append(body, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, file)
}

func clearTOTPSecret() bool { return os.Remove(totpStorePath()) == nil }

// LooksLikeTOTPSecret is the exported form of looksLikeSecret, so main can
// refuse to start on a malformed KINOPUB_AUTH_TOTP_SECRET.
func LooksLikeTOTPSecret(secret string) bool { return looksLikeSecret(secret) }
