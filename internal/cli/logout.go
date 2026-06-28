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

// runLogout clears the stored credentials for server (keychain entry + config
// file). It can't revoke the token server-side (a personal access token isn't
// allowed to delete tokens), so it points the user at the web UI when a
// server-side token remains.
func runLogout(out io.Writer, cfg *Config, server string) error {
	// Credentials may live in the keychain (token cleared from the file), so
	// check there too before declaring nothing to do.
	_, inKeychain := loadToken(server)
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
