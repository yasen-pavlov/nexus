package cli

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/muty/nexus/internal/cliclient"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

func newChatCmd(rf *rootFlags) *cobra.Command {
	var (
		modelFlag   string
		showSources bool
	)
	cmd := &cobra.Command{
		Use:   "chat [id]",
		Short: "Start (or resume) an interactive grounded conversation",
		Long: "Hold a multi-turn grounded conversation. Each answer streams live with a\n" +
			"sources footer, and follow-ups keep the context of the conversation.\n\n" +
			"With no argument it starts a new chat; pass a chat id to resume one (see\n" +
			"`nexus-cli chats list`). The conversation is saved so you can resume it later\n" +
			"— except a brand-new chat you leave without asking anything, which is\n" +
			"discarded. Leave with /exit, /quit, Ctrl-D, or Ctrl-C.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, _, err := authedClient(rf)
			if err != nil {
				return err
			}
			opts := chatOptions{model: modelFlag, showSources: showSources}
			if len(args) == 1 {
				opts.resumeID = args[0]
			}
			return runChat(cmd.Context(), client, cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr(), opts)
		},
	}
	cmd.Flags().StringVarP(&modelFlag, "model", "m", "",
		"model for the session (provider:id, e.g. anthropic:claude-sonnet-4-6); server default if empty")
	cmd.Flags().BoolVar(&showSources, "sources", false, "show all retrieved sources each turn, not just cited ones")
	return cmd
}

type chatOptions struct {
	resumeID    string
	model       string
	showSources bool
}

// runChat runs the interactive REPL: open (or resume) a chat, then loop reading a
// question and streaming its grounded answer until the user leaves. Ctrl-C exits
// (interrupting any in-flight answer) and Ctrl-D / EOF / /exit also leave.
func runChat(ctx context.Context, client *cliclient.Client, in io.Reader, out, errOut io.Writer, opts chatOptions) error {
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	interactive := isInteractive(in)
	chatID, isNew, err := openChatSession(ctx, client, out, interactive, opts)
	if err != nil {
		return err
	}
	turns := 0
	submitted := false // any question sent to the server (even if interrupted mid-answer)
	var lastErr error  // most recent per-turn error, for the exit code
	// A brand-new chat the user never asked anything in is empty clutter —
	// delete it. But once a question has been submitted the server has persisted
	// it (and possibly a partial answer), so keep the chat even if the turn was
	// interrupted by Ctrl-C. Use a fresh context so a cancelled parent still lets
	// cleanup through.
	defer func() {
		if isNew && !submitted {
			delCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = client.DeleteChat(delCtx, chatID)
		}
	}()

	if interactive {
		fprintf(out, "Chatting with Nexus — ask a question; /exit, /quit, Ctrl-D, or Ctrl-C to leave.\n")
	}

	lines := readLines(in)
	for {
		if interactive {
			fprintf(out, "\n› ")
		}
		select {
		case <-ctx.Done():
			return endChat(errOut, chatID, turns, lastErr, isNew, submitted, interactive)
		case line, ok := <-lines:
			if !ok {
				return endChat(errOut, chatID, turns, lastErr, isNew, submitted, interactive)
			}
			res, turnErr := handleLine(ctx, client, out, errOut, chatID, line, opts, &submitted)
			switch res {
			case turnDone:
				return endChat(errOut, chatID, turns, lastErr, isNew, submitted, interactive)
			case turnOK:
				turns++
				lastErr = nil
			case turnContinue:
				if turnErr != nil {
					lastErr = turnErr
				}
			}
		}
	}
}

type turnResult int

const (
	turnContinue turnResult = iota // reprompt without counting a turn
	turnOK                         // an answer completed
	turnDone                       // leave the session
)

// handleLine processes one input line: slash commands, blanks, and otherwise a
// streamed answer. A Ctrl-C mid-answer (ctx cancelled) ends the session; a
// recoverable per-turn error is printed and the loop continues, and is returned
// so the caller can reflect it in the exit code if no turn ever succeeds.
func handleLine(ctx context.Context, client *cliclient.Client, out, errOut io.Writer, chatID, line string, opts chatOptions, submitted *bool) (turnResult, error) {
	q := strings.TrimSpace(line)
	if q == "/exit" || q == "/quit" {
		return turnDone, nil
	}
	if q == "" {
		return turnContinue, nil
	}
	// Mark before streaming: the server persists the user message up front, so an
	// interrupted turn still leaves real content worth keeping.
	*submitted = true
	turnErr := streamTurn(ctx, client, out, errOut, chatID, q, opts)
	if ctx.Err() != nil {
		return turnDone, nil
	}
	if turnErr != nil {
		fprintf(errOut, "error: %v\n", turnErr)
		return turnContinue, turnErr
	}
	return turnOK, nil
}

// streamTurn streams one answer for q, reusing the ask renderer (live text +
// sources footer + meta). It returns the turn's outcome error (nil on success
// or a graceful Ctrl-C).
func streamTurn(ctx context.Context, client *cliclient.Client, out, errOut io.Writer, chatID, q string, opts chatOptions) error {
	r := newAskRenderer(out, errOut, askOptions{model: opts.model, showSources: opts.showSources})
	return r.finish(client.StreamMessage(ctx, chatID, q, opts.model, r.handle))
}

// openChatSession resumes the chat with resumeID or creates a fresh one. isNew
// is true only for a freshly-created chat. For a resume it prints the prior
// conversation as context, but only on a real terminal — a piped resume emits
// just the new answer so it composes cleanly. It returns the known-good
// resumeID (not the GET response's id) for the resumed case.
func openChatSession(ctx context.Context, client *cliclient.Client, out io.Writer, interactive bool, opts chatOptions) (chatID string, isNew bool, err error) {
	if opts.resumeID != "" {
		detail, derr := client.GetChat(ctx, opts.resumeID)
		if derr != nil {
			return "", false, derr
		}
		if interactive {
			formatChatDetail(out, detail)
		}
		return opts.resumeID, false, nil
	}
	chat, cerr := client.CreateChat(ctx)
	if cerr != nil {
		return "", false, fmt.Errorf("create chat: %w", cerr)
	}
	return chat.ID.String(), true, nil
}

// endChat writes the save/resume hint to w (stderr, so a piped stdout stays
// answer-only) unless the chat was empty, and returns the session's exit error:
// the last per-turn error when no turn ever succeeded, else nil. The empty-chat
// delete is handled by runChat's deferred cleanup.
func endChat(w io.Writer, chatID string, turns int, lastErr error, isNew, submitted, interactive bool) error {
	if interactive {
		fprintf(w, "\n")
	}
	if turns > 0 || !isNew || submitted {
		fprintf(w, "Conversation saved: %s\n", chatID)
		if interactive {
			fprintf(w, "Resume it with:  nexus-cli chat %s\n", chatID)
		}
	}
	if turns == 0 {
		return lastErr
	}
	return nil
}

// isInteractive reports whether in is a real terminal (vs piped input).
func isInteractive(in io.Reader) bool {
	f, ok := in.(*os.File)
	return ok && term.IsTerminal(int(f.Fd()))
}

// readLines pumps lines from in onto a channel (closed at EOF) so the REPL can
// select between input and ctx cancellation — a blocking stdin read can't be
// interrupted by Ctrl-C directly.
func readLines(in io.Reader) <-chan string {
	ch := make(chan string)
	go func() {
		defer close(ch)
		r := bufio.NewReader(in)
		for {
			line, err := r.ReadString('\n')
			if line != "" {
				ch <- line
			}
			if err != nil {
				return
			}
		}
	}()
	return ch
}
