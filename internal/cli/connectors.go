package cli

import (
	"errors"
	"net/http"

	"github.com/muty/nexus/internal/cliclient"
	"github.com/spf13/cobra"
)

func newConnectorsCmd(rf *rootFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "connectors",
		Short: "List connectors and trigger syncs",
		Args:  cobra.NoArgs,
	}
	cmd.AddCommand(newConnectorsListCmd(rf), newConnectorsSyncCmd(rf), newConnectorsStatusCmd(rf))
	return cmd
}

func newConnectorsListCmd(rf *rootFlags) *cobra.Command {
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List your connectors",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, _, err := authedClient(rf)
			if err != nil {
				return err
			}
			conns, err := client.ListConnectors(cmd.Context())
			if err != nil {
				return err
			}
			if jsonOut {
				return writeJSON(cmd.OutOrStdout(), conns)
			}
			formatConnectors(cmd.OutOrStdout(), conns)
			return nil
		},
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, flagJSONUsage)
	return cmd
}

func newConnectorsSyncCmd(rf *rootFlags) *cobra.Command {
	var (
		all     bool
		jsonOut bool
	)
	cmd := &cobra.Command{
		Use:   "sync [id]",
		Short: "Trigger a sync for one connector (by id) or every eligible one (--all)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, _, err := authedClient(rf)
			if err != nil {
				return err
			}
			if all {
				if len(args) > 0 {
					return errors.New("pass either a connector id or --all, not both")
				}
				return runSyncAll(cmd, client, jsonOut)
			}
			if len(args) != 1 {
				return errors.New("provide a connector id, or use --all")
			}
			return runSyncOne(cmd, client, args[0], jsonOut)
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "sync all eligible connectors")
	cmd.Flags().BoolVar(&jsonOut, "json", false, flagJSONUsage)
	return cmd
}

func runSyncAll(cmd *cobra.Command, client *cliclient.Client, jsonOut bool) error {
	jobs, err := client.SyncAll(cmd.Context())
	if err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	if jsonOut {
		return writeJSON(out, jobs)
	}
	if len(jobs) == 0 {
		fprintf(out, "No connectors started (none eligible or all already running).\n")
		return nil
	}
	for i := range jobs {
		fprintf(out, "Started sync for %s (job %s)\n", jobs[i].ConnectorName, jobs[i].ID)
	}
	return nil
}

func runSyncOne(cmd *cobra.Command, client *cliclient.Client, id string, jsonOut bool) error {
	job, err := client.TriggerSync(cmd.Context(), id)
	if err != nil {
		// A sync already in flight is the desired state, not a failure.
		var apiErr *cliclient.APIError
		if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusConflict {
			fprintf(cmd.OutOrStdout(), "A sync is already running for this connector.\n")
			return nil
		}
		return err
	}
	if jsonOut {
		return writeJSON(cmd.OutOrStdout(), job)
	}
	fprintf(cmd.OutOrStdout(), "Started sync for %s (job %s)\n", job.ConnectorName, job.ID)
	return nil
}

func newConnectorsStatusCmd(rf *rootFlags) *cobra.Command {
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show running and recently-finished sync jobs",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, _, err := authedClient(rf)
			if err != nil {
				return err
			}
			jobs, err := client.ListSyncJobs(cmd.Context())
			if err != nil {
				return err
			}
			if jsonOut {
				return writeJSON(cmd.OutOrStdout(), jobs)
			}
			formatSyncJobs(cmd.OutOrStdout(), jobs)
			return nil
		},
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, flagJSONUsage)
	return cmd
}
