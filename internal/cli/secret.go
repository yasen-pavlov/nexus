package cli

import (
	"os"
	"os/user"
	"runtime"

	"github.com/zalando/go-keyring"
)

// keyringService is the keychain service name under which nexus-cli stores
// tokens — one entry per server URL, so credentials for different Nexus servers
// coexist.
const keyringService = "nexus-cli"

// Keyring access is wrapped in package vars so tests can substitute an in-memory
// fake and never touch the real OS secret service.
var (
	keyringSet    = keyring.Set
	keyringGet    = keyring.Get
	keyringDelete = keyring.Delete

	// sessionBusSocket returns the well-known per-user DBus session-bus socket
	// path, or "" if the user can't be resolved. A package var so tests can point
	// it at a controlled path.
	sessionBusSocket = func() string {
		u, err := user.Current()
		if err != nil {
			return ""
		}
		return "/run/user/" + u.Uid + "/bus"
	}

	// keyringUsable reports whether the OS secret service is reachable. On Linux
	// it requires a DBus *session* bus (a desktop session); without one we fall
	// back to the 0600 file rather than risk go-keyring autolaunching a throwaway
	// dbus-launch or blocking. We accept either an explicit DBUS_SESSION_BUS_ADDRESS
	// or the presence of the session-bus socket (so a desktop shell that didn't
	// export the var is still detected). NOT XDG_RUNTIME_DIR — that's present on
	// headless/SSH boxes that have no bus. macOS/Windows always have a backend.
	keyringUsable = func() bool {
		if runtime.GOOS != "linux" {
			return true
		}
		if os.Getenv("DBUS_SESSION_BUS_ADDRESS") != "" {
			return true
		}
		if p := sessionBusSocket(); p != "" {
			if _, err := os.Stat(p); err == nil {
				return true
			}
		}
		return false
	}
)

// storeToken saves token to the OS keychain keyed by server. It returns false
// (no error) when the keychain is unavailable or the write fails, signalling the
// caller to fall back to the 0600 config file.
func storeToken(server, token string) bool {
	if !keyringUsable() {
		return false
	}
	return keyringSet(keyringService, server, token) == nil
}

// loadToken returns the keychain token for server, or ("", false) when absent or
// the keychain is unavailable.
func loadToken(server string) (string, bool) {
	if !keyringUsable() {
		return "", false
	}
	tok, err := keyringGet(keyringService, server)
	if err != nil || tok == "" {
		return "", false
	}
	return tok, true
}

// removeToken deletes server's keychain entry, ignoring a missing entry.
func removeToken(server string) {
	if !keyringUsable() {
		return
	}
	_ = keyringDelete(keyringService, server)
}
