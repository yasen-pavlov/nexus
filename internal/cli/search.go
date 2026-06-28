package cli

import (
	"errors"
	"strings"

	"github.com/muty/nexus/internal/cliclient"
	"github.com/spf13/cobra"
)

// errNotLoggedIn is returned when a command needs auth but no token is resolvable.
// It shares its text with the MCP server's tool result via cliclient.
var errNotLoggedIn = errors.New(cliclient.NotAuthenticatedHint)

func newSearchCmd(rf *rootFlags) *cobra.Command {
	var (
		limit       int
		offset      int
		sources     []string
		sourceNames []string
		dateFrom    string
		dateTo      string
		jsonOut     bool
		explain     bool
	)
	cmd := &cobra.Command{
		Use:   "search <query>",
		Short: "Search across all indexed documents",
		Long: "Run a hybrid (BM25 + vector) search and print the ranked results.\n" +
			"Results are scoped to your user plus any shared connectors.",
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, _, err := authedClient(rf)
			if err != nil {
				return err
			}
			params := cliclient.SearchParams{
				Query:       strings.Join(args, " "),
				Limit:       limit,
				Offset:      offset,
				Sources:     sources,
				SourceNames: sourceNames,
				DateFrom:    dateFrom,
				DateTo:      dateTo,
				Explain:     explain,
			}
			result, err := client.Search(cmd.Context(), params)
			if err != nil {
				return err
			}
			if jsonOut {
				return writeJSON(cmd.OutOrStdout(), result)
			}
			return formatSearchResults(cmd.OutOrStdout(), result, explain)
		},
	}
	cmd.Flags().IntVarP(&limit, "limit", "n", 20, "maximum number of results")
	cmd.Flags().IntVar(&offset, "offset", 0, "pagination offset")
	cmd.Flags().StringSliceVarP(&sources, "source", "s", nil,
		"filter by source type, repeatable (e.g. -s imap -s telegram)")
	cmd.Flags().StringSliceVar(&sourceNames, "source-name", nil,
		"filter by source name, repeatable")
	cmd.Flags().StringVar(&dateFrom, "from", "", "only results on/after this date (YYYY-MM-DD)")
	cmd.Flags().StringVar(&dateTo, "to", "", "only results on/before this date (YYYY-MM-DD)")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "output raw JSON instead of a formatted list")
	cmd.Flags().BoolVar(&explain, "explain", false, "include the per-hit score breakdown")
	return cmd
}
