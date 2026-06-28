package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"

	"github.com/muty/nexus/internal/cliclient"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

func newLoginCmd(rf *rootFlags) *cobra.Command {
	var (
		username  string
		tokenFlag string
	)
	cmd := &cobra.Command{
		Use:   "login",
		Short: "Authenticate to a Nexus server and store a personal access token",
		Long: "Authenticate to a Nexus server and store a long-lived personal access " +
			"token (preferring the OS keychain, falling back to a 0600 config file).\n\n" +
			"Modes:\n" +
			"  • Interactive (default): prompts for username + password, then mints a\n" +
			"    token named for this host.\n" +
			"  • Paste a token: --token nexus_pat_... stores an existing token minted\n" +
			"    in the web UI (Account → Tokens). Passing it literally exposes it via\n" +
			"    shell history and the process arg list — prefer --token - to read it\n" +
			"    from stdin, e.g.  nexus-cli login --token - <<<\"$PAT\".",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := LoadConfig()
			if err != nil {
				return err
			}
			server := resolveServerURL(rf.server, cfg)
			return runLogin(cmd.Context(), cmd.InOrStdin(), cmd.OutOrStdout(), server, username, tokenFlag)
		},
	}
	cmd.Flags().StringVarP(&username, "username", "u", "", "username (prompted if omitted)")
	cmd.Flags().StringVar(&tokenFlag, "token", "",
		"store this personal access token instead of logging in ('-' reads it from stdin)")
	return cmd
}

// runLogin obtains a token (pasted literally, read from stdin, or minted via
// username/password), validates it, and persists it alongside the resolved
// server URL.
func runLogin(ctx context.Context, in io.Reader, out io.Writer, server, username, pastedToken string) error {
	warnInsecureServer(out, server)

	token, tokenID, err := obtainToken(ctx, in, out, server, username, pastedToken)
	if err != nil {
		return err
	}

	// Validate whatever token we now hold by resolving the current user — this
	// also captures the username for the pasted-token path.
	user, err := cliclient.New(server, token).Me(ctx)
	if err != nil {
		return fmt.Errorf("validate token: %w", err)
	}

	where, err := persistCredentials(&Config{ServerURL: server, TokenID: tokenID, Username: user.Username}, token)
	if err != nil {
		return err
	}
	fprintf(out, "Logged in as %s (%s) at %s\nToken stored in %s.\n", user.Username, user.Role, server, where)
	if os.Getenv(envToken) != "" {
		fprintf(out, "Warning: %s is set in your environment and overrides stored credentials for every command; unset it to use the token just stored.\n", envToken)
	}
	return nil
}

// obtainToken resolves the token to store: from stdin (--token -), from the
// literal flag, or by minting one via an interactive password login. tokenID is
// non-empty only for the minted path (a pasted token's server-side id is unknown).
func obtainToken(ctx context.Context, in io.Reader, out io.Writer, server, username, pastedToken string) (token, tokenID string, err error) {
	switch {
	case pastedToken == "-":
		token, err = readLine(in, "no token read from stdin")
		return token, "", err
	case pastedToken != "":
		token = strings.TrimSpace(pastedToken)
		if token == "" {
			return "", "", errors.New("empty token")
		}
		return token, "", nil
	default:
		creds, err := promptCredentials(in, out, username)
		if err != nil {
			return "", "", err
		}
		return mintToken(ctx, server, creds.username, creds.password, tokenName())
	}
}

// readLine reads and trims a single line from r, erroring (with emptyMsg) when
// nothing is read.
func readLine(r io.Reader, emptyMsg string) (string, error) {
	line, err := bufio.NewReader(r).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", fmt.Errorf("read from stdin: %w", err)
	}
	line = strings.TrimSpace(line)
	if line == "" {
		return "", errors.New(emptyMsg)
	}
	return line, nil
}

// warnInsecureServer notes when a token will be sent over plain HTTP to a
// non-local host, where it could be sniffed on the wire.
func warnInsecureServer(out io.Writer, server string) {
	u, err := url.Parse(server)
	if err != nil || u.Scheme != "http" {
		return
	}
	switch u.Hostname() {
	case "localhost", "127.0.0.1", "::1":
		return
	}
	fprintf(out, "Warning: %s uses plain HTTP; your token is sent unencrypted over the network. Use https:// for non-local servers.\n", server)
}

// mintToken logs in with username/password and mints a CLI token, returning the
// plaintext token and its server-side id. Separated from prompting so the
// network flow is unit-testable.
func mintToken(ctx context.Context, server, username, password, name string) (token, tokenID string, err error) {
	jwt, err := cliclient.New(server, "").Login(ctx, username, password)
	if err != nil {
		return "", "", err
	}
	pat, meta, err := cliclient.New(server, jwt).CreateToken(ctx, name)
	if err != nil {
		return "", "", err
	}
	if meta != nil {
		tokenID = meta.ID.String()
	}
	return pat, tokenID, nil
}

type credentials struct {
	username string
	password string
}

// promptCredentials reads a username (unless preset) and password from in,
// writing prompts to out. Password input is masked only when in is the real
// terminal; piped input (tests, scripts) is read as a plain line.
func promptCredentials(in io.Reader, out io.Writer, username string) (credentials, error) {
	reader := bufio.NewReader(in)
	if username == "" {
		fprintf(out, "Username: ")
		line, err := reader.ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return credentials{}, fmt.Errorf("read username: %w", err)
		}
		username = strings.TrimSpace(line)
	}
	if username == "" {
		return credentials{}, errors.New("username is required")
	}

	password, err := readPassword(in, reader, out)
	if err != nil {
		return credentials{}, err
	}
	if password == "" {
		return credentials{}, errors.New("password is required")
	}
	return credentials{username: username, password: password}, nil
}

// terminalPassword reads a masked password when f is an interactive terminal.
// handled is false when f is not a terminal, signalling the caller to fall back
// to a plain line read. It is a package var so tests can drive readPassword
// without a real TTY (the term.ReadPassword path can't run unattended).
var terminalPassword = func(f *os.File, out io.Writer) (pw string, handled bool, err error) {
	if !term.IsTerminal(int(f.Fd())) {
		return "", false, nil
	}
	fprintf(out, "Password: ")
	b, err := term.ReadPassword(int(f.Fd()))
	fprintf(out, "\n")
	if err != nil {
		return "", true, fmt.Errorf("read password: %w", err)
	}
	return strings.TrimSpace(string(b)), true, nil
}

// readPassword reads a password without echo when in is an interactive terminal,
// and falls back to a buffered line read otherwise (so piped input still works).
func readPassword(in io.Reader, reader *bufio.Reader, out io.Writer) (string, error) {
	if f, ok := in.(*os.File); ok {
		if pw, handled, err := terminalPassword(f, out); handled {
			return pw, err
		}
	}
	line, err := reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", fmt.Errorf("read password: %w", err)
	}
	return strings.TrimSpace(line), nil
}

// tokenName labels the minted token with this host so it's identifiable in the
// web UI's token list.
func tokenName() string {
	host, err := os.Hostname()
	if err != nil || host == "" {
		return "nexus-cli"
	}
	return "nexus-cli (" + host + ")"
}
