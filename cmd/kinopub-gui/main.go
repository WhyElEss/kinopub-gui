// Command kinopub-gui serves a self-contained web interface for the kinopub
// downloader. It reuses the same Go engine as the CLI and streams real progress
// to the browser. Run it and a browser tab opens automatically.
package main

import (
	"context"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"github.com/ZioSHik/kinopub-gui/internal/gui"
	"github.com/ZioSHik/kinopub-gui/internal/lib/termx"
	"github.com/ZioSHik/kinopub-gui/web"
)

var version = "dev"

func main() {
	os.Exit(run())
}

func run() int {
	var (
		addr         string
		noOpen       bool
		showVersion  bool
		lan          bool
		noSelfUpdate bool
		publicHost   string
		hashPassword bool
	)
	flag.StringVar(&addr, "addr", "127.0.0.1:8765", "address to listen on (host:port)")
	flag.BoolVar(&noOpen, "no-open", false, "do not open the browser automatically")
	flag.BoolVar(&lan, "lan", false, "accept requests from the local network too (use with -addr 0.0.0.0:8765; without a password, anyone on the LAN gets full access)")
	flag.StringVar(&publicHost, "public-host", os.Getenv("KINOPUB_PUBLIC_HOST"), "public hostname this server may be addressed by, e.g. a Cloudflare Tunnel origin (requires KINOPUB_AUTH_PASSWORD_HASH)")
	flag.BoolVar(&hashPassword, "hash-password", false, "prompt for a password and print the KINOPUB_AUTH_PASSWORD_HASH line, then exit")
	flag.BoolVar(&noSelfUpdate, "no-self-update", false, "disable the in-app updater (for container/package installs)")
	flag.BoolVar(&showVersion, "version", false, "print version and exit")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "kinopub-gui %s — web interface for the kinopub downloader\n\n", version)
		fmt.Fprintf(os.Stderr, "Usage:\n  kinopub-gui [flags]\n\nFlags:\n")
		flag.PrintDefaults()
	}
	flag.Parse()

	if showVersion {
		fmt.Printf("kinopub-gui %s\n", version)
		return 0
	}

	if hashPassword {
		return printPasswordHash()
	}

	srv := gui.NewServer(version, web.Dist())
	srv.SetAllowLAN(lan)
	srv.SetSelfUpdate(!noSelfUpdate)

	// A password hash that exists but cannot be recognised is a startup ERROR,
	// not a warning: quietly serving with no login because a paste got
	// truncated is the failure nobody notices, and this server holds a kino.pub
	// session and can write gigabytes into a media library.
	if raw := os.Getenv("KINOPUB_AUTH_PASSWORD_HASH"); raw != "" && !gui.LooksLikeHash(raw) {
		fmt.Fprintln(os.Stderr,
			"kinopub-gui: KINOPUB_AUTH_PASSWORD_HASH is not a hash produced by "+
				"`kinopub-gui -hash-password` (expected scrypt$N$r$p$salt$key). "+
				"Refusing to start with a password nobody could match.")
		return 1
	}
	// Same reasoning for the second factor: if it was asked for in the
	// environment and cannot be read, running anyway means running with less
	// protection than was configured.
	if secret := os.Getenv("KINOPUB_AUTH_TOTP_SECRET"); secret != "" && !gui.LooksLikeTOTPSecret(secret) {
		fmt.Fprintln(os.Stderr,
			"kinopub-gui: KINOPUB_AUTH_TOTP_SECRET is not a usable base32 secret "+
				"(at least 16 bytes once decoded). Refusing to start rather than "+
				"quietly serving without the second factor you asked for.")
		return 1
	}

	// -public-host is the whole reason this server can be reached by a name
	// public DNS resolves. Setting it without a password would publish a
	// credential-holding downloader to the internet, so it is refused outright
	// — this is the one failure that must be impossible.
	if publicHost != "" {
		if !srv.AuthEnabled() {
			fmt.Fprintf(os.Stderr,
				"kinopub-gui: -public-host %s needs a password. Run "+
					"`kinopub-gui -hash-password` and set KINOPUB_AUTH_PASSWORD_HASH. "+
					"Refusing to publish a server with no login.\n", publicHost)
			return 1
		}
		srv.SetPublicHost(publicHost)
	}
	// Followed series are checked for new episodes in the background from here on.
	srv.StartWatcher()

	// Bind the listener up front so we know the final address (and can fall
	// back to an ephemeral port if the preferred one is taken).
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		host, _, _ := net.SplitHostPort(addr)
		if host == "" {
			host = "127.0.0.1"
		}
		fmt.Fprintf(os.Stderr, "kinopub-gui: %s is unavailable (%v); using an ephemeral port\n", addr, err)
		ln, err = net.Listen("tcp", net.JoinHostPort(host, "0"))
		if err != nil {
			fmt.Fprintf(os.Stderr, "kinopub-gui: cannot listen: %v\n", err)
			return 1
		}
	}

	url := "http://" + ln.Addr().String()
	httpSrv := &http.Server{
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	// Let the update endpoint restart us: it signals here, and we re-exec the
	// freshly installed binary on the same address (with -no-open) so the open
	// browser tab simply reconnects over SSE.
	restartCh := make(chan struct{}, 1)
	srv.SetRestart(func() {
		select {
		case restartCh <- struct{}{}:
		default:
		}
	})

	banner(url)
	authBanner(srv, publicHost)

	errCh := make(chan error, 1)
	go func() { errCh <- httpSrv.Serve(ln) }()

	if !noOpen {
		go func() {
			time.Sleep(300 * time.Millisecond)
			openBrowser(url)
		}()
	}

	// Block until the server stops. On macOS (inside a .app) runApp runs a tiny
	// Cocoa application so there's a real Dock icon and ⌘Q; elsewhere it just
	// waits on the server, a self-update restart, or an interrupt signal.
	return runApp(httpSrv, ln, errCh, restartCh)
}

// runHeadless blocks until the server stops — a fatal serve error, a self-update
// restart, or an interrupt (Ctrl-C / SIGTERM) — and returns a process exit code.
// It is the whole lifecycle for CLI/headless runs and on platforms without a
// native app shell.
func runHeadless(srv *http.Server, ln net.Listener, errCh <-chan error, restartCh <-chan struct{}) int {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	select {
	case err := <-errCh:
		if err != nil && err != http.ErrServerClosed {
			fmt.Fprintf(os.Stderr, "kinopub-gui: server error: %v\n", err)
			return 1
		}
	case <-restartCh:
		return performRestart(ln)
	case <-sigCh:
		fmt.Fprintln(os.Stderr, "\nkinopub-gui: shutting down…")
		gracefulShutdown(srv)
	}
	return 0
}

// gracefulShutdown asks the server to stop, giving in-flight requests a few
// seconds to finish.
func gracefulShutdown(srv *http.Server) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(ctx)
}

// performRestart re-execs the freshly installed binary on the same address (with
// -no-open) so the open browser tab simply reconnects over SSE. It returns a
// process exit code; on Unix a successful re-exec replaces the image and does
// not return.
func performRestart(ln net.Listener) int {
	fmt.Fprintln(os.Stderr, "\nkinopub-gui: update installed — restarting…")
	boundAddr := ln.Addr().String()
	_ = ln.Close() // release the port for the new process
	exe, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "kinopub-gui: cannot locate executable: %v\n", err)
		return 1
	}
	if err := reexec(exe, []string{"-addr", boundAddr, "-no-open"}); err != nil {
		fmt.Fprintf(os.Stderr, "kinopub-gui: restart failed (please relaunch manually): %v\n", err)
		return 1
	}
	return 0
}

func banner(url string) {
	reset, bold, cyan, dim := "", "", "", ""
	if useColor(os.Stdout) {
		reset, bold, cyan, dim = "\033[0m", "\033[1m", "\033[36m", "\033[2m"
	}
	fmt.Printf("\n  %s%skinopub%s %sweb interface%s\n", bold, cyan, reset, dim, reset)
	fmt.Printf("  %s▸%s  %s%s%s\n", cyan, reset, bold, url, reset)
	fmt.Printf("  %sPress Ctrl-C to stop%s\n\n", dim, reset)
}

// useColor reports whether ANSI colors should be emitted to f: only when it is a
// real terminal and NO_COLOR is unset. On Windows it best-effort enables virtual
// terminal processing so modern consoles render the escapes instead of garbage.
func useColor(f *os.File) bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	if !termx.IsTTY(f) {
		return false
	}
	return enableVT(f)
}

// openBrowser opens the given URL in the default browser, best-effort.
func openBrowser(url string) {
	var cmd string
	var args []string
	switch runtime.GOOS {
	case "darwin":
		cmd = "open"
		args = []string{url}
	case "windows":
		cmd = "rundll32"
		args = []string{"url.dll,FileProtocolHandler", url}
	default: // linux, bsd, …
		cmd = "xdg-open"
		args = []string{url}
	}
	_ = exec.Command(cmd, args...).Start()
}
