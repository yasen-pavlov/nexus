package cli

import (
	"io"

	"github.com/spf13/cobra"
)

func newLogoutCmd(rf *rootFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "logout",
		Short: "Remove the stored credentials from this machine",
		Long: "Clear the locally stored token (keychain entry + config file).\n\n" +
			"By default it targets the logged-in server; pass --server (or set " +
			"NEXUS_URL) to clear an orphaned keychain entry for a different server.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := LoadConfig()
			if err != nil {
				return err
			}
			server := resolveServerURL(rf.server, cfg)
			return runLogout(cmd.OutOrStdout(), cfg, server)
		},
	}
}

// runLogout clears the stored credentials for server: always the keychain
// entry, and — only for the logged-in server — the config file too. Passing
// --server (or NEXUS_URL) for a DIFFERENT server clears just that server's
// orphaned keychain entry and leaves the logged-in server's config file intact,
// so a cleanup of server B never logs you out of server A. It can't revoke the
// token server-side (a personal access token isn't allowed to delete tokens),
// so it points the user at the web UI when a server-side token remains.
func runLogout(out io.Writer, cfg *Config, server string) error {
	// Credentials may live in the keychain (token cleared from the file), so
	// check there too before declaring nothing to do.
	_, inKeychain := loadToken(server)

	if !sameServer(cfg.ServerURL, server) {
		// Orphan-keychain cleanup for a different server. Consult ONLY that
		// server's keychain entry — cfg.Token/Username/TokenID belong to the
		// logged-in server and must not be touched or reported here.
		if !inKeychain {
			fprintf(out, "No stored credentials.\n")
			return nil
		}
		removeToken(server)
		fprintf(out, "Local credentials cleared.\n")
		return nil
	}

	// Full logout of the logged-in server: keychain entry + config file.
	if cfg.Token == "" && cfg.Username == "" && !inKeychain {
		fprintf(out, "No stored credentials.\n")
		return nil
	}
	removeToken(server)
	if err := ClearConfig(); err != nil {
		return err
	}
	fprintf(out, "Local credentials cleared.\n")
	if cfg.TokenID != "" {
		fprintf(out, "Note: the token still exists server-side. Revoke it in the web UI (Account → Tokens) if needed.\n")
	}
	return nil
}
