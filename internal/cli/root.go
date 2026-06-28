package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// rootFlags holds flags shared across commands.
type rootFlags struct {
	server string
}

// Execute builds and runs the root command, exiting non-zero on error. main()
// passes the build-time version vars in. Cobra prints the error itself, so this
// only sets the exit code.
func Execute(version, commit, date string) {
	if err := NewRootCmd(version, commit, date).Execute(); err != nil {
		os.Exit(1)
	}
}

// NewRootCmd assembles the command tree. Exported (not just Execute) so tests can
// drive it with custom args, input, and output buffers.
func NewRootCmd(version, commit, date string) *cobra.Command {
	flags := &rootFlags{}

	root := &cobra.Command{
		Use:               "nexus-cli",
		Short:             "Command-line client for a Nexus personal search server",
		Version:           fmt.Sprintf("%s (commit %s, built %s)", version, commit, date),
		SilenceUsage:      true,
		DisableAutoGenTag: true,
	}
	root.PersistentFlags().StringVar(&flags.server, "server", "",
		"Nexus server URL (overrides NEXUS_URL and stored config)")

	root.AddCommand(newLoginCmd(flags))
	root.AddCommand(newLogoutCmd(flags))
	root.AddCommand(newSearchCmd(flags))

	return root
}
