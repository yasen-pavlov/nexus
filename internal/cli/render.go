package cli

import (
	"io"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/muty/nexus/internal/cliclient"
	"github.com/muty/nexus/internal/model"
)

// dateTimeLayout is the table timestamp format (local time).
const dateTimeLayout = "2006-01-02 15:04"

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

// dash renders an empty string as "-" so table cells aren't blank.
func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func formatTimePtr(t *time.Time) string {
	if t == nil || t.IsZero() {
		return "never"
	}
	return t.Format(dateTimeLayout)
}

// oneLine collapses all runs of whitespace to single spaces.
func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// truncate caps s at max runes (rune-safe, so multibyte content isn't split),
// appending an ellipsis when shortened.
func truncate(s string, max int) string {
	r := []rune(s)
	if len(r) > max {
		return string(r[:max]) + "…"
	}
	return s
}

func formatChats(w io.Writer, chats []cliclient.ChatListEntry, total int) {
	if len(chats) == 0 {
		fprintf(w, "No chats.\n")
		return
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fprintf(tw, "ID\tUPDATED\tMODEL\tTITLE\n")
	for i := range chats {
		c := &chats[i]
		title := strings.TrimSpace(c.Title)
		if title == "" {
			title = strings.TrimSpace(c.FirstMessagePreview)
		}
		if title == "" {
			title = "(untitled)"
		}
		fprintf(tw, "%s\t%s\t%s\t%s\n",
			c.ID.String(), c.UpdatedAt.Format(dateTimeLayout),
			dash(c.DefaultModel), truncate(oneLine(title), 60))
	}
	_ = tw.Flush()
	fprintf(w, "\n%d of %d chats\n", len(chats), total)
}

func formatChatDetail(w io.Writer, d *cliclient.ChatDetail) {
	title := strings.TrimSpace(d.Chat.Title)
	if title == "" {
		title = "(untitled)"
	}
	fprintf(w, "%s\n", title)
	fprintf(w, "id: %s · model: %s · updated: %s\n",
		d.Chat.ID, dash(d.Chat.DefaultModel), d.Chat.UpdatedAt.Format(dateTimeLayout))
	if len(d.Messages) == 0 {
		fprintf(w, "\n(no messages)\n")
		return
	}
	for i := range d.Messages {
		m := &d.Messages[i]
		fprintf(w, "\n── %s ──\n%s\n", m.Role, strings.TrimSpace(m.Content))
		if m.Role == model.ChatRoleAssistant && len(m.Evidence) > 0 {
			fprintf(w, "(%d sources)\n", len(m.Evidence))
		}
	}
}

func formatConnectors(w io.Writer, conns []cliclient.Connector) {
	if len(conns) == 0 {
		fprintf(w, "No connectors.\n")
		return
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fprintf(tw, "ID\tTYPE\tNAME\tENABLED\tSCHEDULE\tSHARED\tSTATUS\tLAST RUN\n")
	for i := range conns {
		c := &conns[i]
		fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			c.ID.String(), c.Type, c.Name, yesNo(c.Enabled),
			dash(c.Schedule), yesNo(c.Shared), c.Status, formatTimePtr(c.LastRun))
	}
	_ = tw.Flush()
}

func formatSyncJobs(w io.Writer, jobs []cliclient.SyncJob) {
	if len(jobs) == 0 {
		fprintf(w, "No active or recent sync jobs.\n")
		return
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fprintf(tw, "CONNECTOR\tTYPE\tSTATUS\tDOCS\tERRORS\tSTARTED\n")
	for i := range jobs {
		j := &jobs[i]
		fprintf(tw, "%s\t%s\t%s\t%d/%d\t%d\t%s\n",
			j.ConnectorName, j.ConnectorType, j.Status,
			j.DocsProcessed, j.DocsTotal, j.Errors, j.StartedAt.Format("15:04:05"))
	}
	_ = tw.Flush()
}
