package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/term"

	"github.com/ZioSHik/kinopub-gui/internal/gui"
)

// -hash-password turns a password into the line KINOPUB_AUTH_PASSWORD_HASH
// wants.
//
//	kinopub-gui -hash-password                 (prompts twice, echo off)
//	echo 's3cret' | kinopub-gui -hash-password (for scripted setup)
//
// The plaintext is never written anywhere: not to a file, not to the shell
// history, not to the terminal. What it prints is the line to paste into .env.

// The login is the only thing between the internet and a server that holds a
// kino.pub session and can fill a disk, so this floor is deliberate rather than
// advisory.
const minPasswordLength = 12

func printPasswordHash() int {
	password, err := readPasswordTwice()
	if err != nil {
		fmt.Fprintf(os.Stderr, "kinopub-gui: %v\n", err)
		return 1
	}
	if len(password) < minPasswordLength {
		fmt.Fprintf(os.Stderr,
			"kinopub-gui: the password must be at least %d characters — it is the "+
				"only barrier in front of this server\n", minPasswordLength)
		return 1
	}

	hash, err := gui.HashPassword(password)
	if err != nil {
		fmt.Fprintf(os.Stderr, "kinopub-gui: %v\n", err)
		return 1
	}
	// Verify what is about to be printed actually matches what was typed. A
	// hash that does not round-trip would lock the operator out of their own
	// box, and finding that out at the login screen is the expensive way.
	if !gui.LooksLikeHash(hash) || !gui.VerifyPassword(password, hash) {
		fmt.Fprintln(os.Stderr,
			"kinopub-gui: the generated hash failed its own verification — refusing to print it")
		return 1
	}

	// Say this before printing the lines, and say it plainly: run inside the
	// container — which is how it is normally run — this process cannot write
	// .env even if it wanted to, because compose mounts only /config and the
	// media library. An operator who assumes "it saved" ends up with a server
	// that never got the password.
	out := os.Stdout
	fmt.Fprint(out, "\n=== NOTHING HAS BEEN SAVED ===\n")
	fmt.Fprint(out, "Copy the line below into deploy/.env ON THE HOST by hand, using an\n")
	fmt.Fprint(out, "editor rather than a shell redirect, so the hash stays out of your\n")
	fmt.Fprint(out, "shell history. Then: docker compose up -d kinopub-gui\n\n")
	fmt.Fprintf(out, "KINOPUB_AUTH_PASSWORD_HASH=%s\n\n", hash)
	fmt.Fprint(out, "The username defaults to \"admin\"; set KINOPUB_AUTH_USER to change it.\n")
	fmt.Fprint(out, "Add the second factor from Settings → Security once you are signed in.\n")
	fmt.Fprint(out, "Setting the hash turns the login on for EVERY request, LAN included.\n")
	return 0
}

func readPasswordTwice() (string, error) {
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		// Piped: one line, no confirmation to compare it against.
		raw, err := io.ReadAll(bufio.NewReader(os.Stdin))
		if err != nil {
			return "", err
		}
		return strings.TrimRight(string(raw), "\r\n"), nil
	}

	fmt.Fprint(os.Stderr, "Password: ")
	first, err := term.ReadPassword(fd)
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", err
	}
	fmt.Fprint(os.Stderr, "Repeat: ")
	second, err := term.ReadPassword(fd)
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", err
	}
	if string(first) != string(second) {
		return "", fmt.Errorf("the two entries differ")
	}
	return string(first), nil
}

// authBanner states what the login is doing, right under the address. A server
// that is public but whose second factor silently failed to load is exactly the
// thing worth reading in a log.
func authBanner(srv *gui.Server, publicHost string) {
	if !srv.AuthEnabled() {
		if publicHost != "" {
			return // unreachable: main refuses that combination
		}
		fmt.Fprintln(os.Stderr, "  no login configured — keep this port off the internet")
		return
	}
	mode := "password"
	if srv.TOTPEnabled() {
		mode = "password + TOTP"
	}
	if publicHost != "" {
		fmt.Fprintf(os.Stderr, "  login: %s — also reachable as https://%s\n", mode, publicHost)
	} else {
		fmt.Fprintf(os.Stderr, "  login: %s\n", mode)
	}
}
