package cli

import (
	"github.com/spf13/cobra"
)

func newChatsCmd(rf *rootFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "chats",
		Short: "List, view, and delete your chats",
		Args:  cobra.NoArgs,
	}
	cmd.AddCommand(newChatsListCmd(rf), newChatsGetCmd(rf), newChatsDeleteCmd(rf))
	return cmd
}

func newChatsListCmd(rf *rootFlags) *cobra.Command {
	var (
		limit   int
		offset  int
		jsonOut bool
	)
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List your chats (most recent first)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, _, err := authedClient(rf)
			if err != nil {
				return err
			}
			chats, total, err := client.ListChats(cmd.Context(), limit, offset)
			if err != nil {
				return err
			}
			if jsonOut {
				return writeJSON(cmd.OutOrStdout(), chats)
			}
			formatChats(cmd.OutOrStdout(), chats, total)
			return nil
		},
	}
	cmd.Flags().IntVarP(&limit, "limit", "n", 50, "maximum chats to list")
	cmd.Flags().IntVar(&offset, "offset", 0, "pagination offset")
	cmd.Flags().BoolVar(&jsonOut, "json", false, flagJSONUsage)
	return cmd
}

func newChatsGetCmd(rf *rootFlags) *cobra.Command {
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "get <id>",
		Short: "Show a chat and its messages",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, _, err := authedClient(rf)
			if err != nil {
				return err
			}
			detail, err := client.GetChat(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			if jsonOut {
				return writeJSON(cmd.OutOrStdout(), detail)
			}
			formatChatDetail(cmd.OutOrStdout(), detail)
			return nil
		},
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, flagJSONUsage)
	return cmd
}

func newChatsDeleteCmd(rf *rootFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "delete <id>",
		Short: "Delete a chat and its messages",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, _, err := authedClient(rf)
			if err != nil {
				return err
			}
			if err := client.DeleteChat(cmd.Context(), args[0]); err != nil {
				return err
			}
			fprintf(cmd.OutOrStdout(), "Deleted chat %s\n", args[0])
			return nil
		},
	}
}
