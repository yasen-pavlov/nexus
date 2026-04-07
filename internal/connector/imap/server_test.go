package imap

import (
	"fmt"
	"io"
	"log"
	"net"
	"strings"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapserver"
)

// fakeMessage holds the data for a message in the fake server.
type fakeMessage struct {
	uid      imap.UID
	date     time.Time
	envelope *imap.Envelope
	body     []byte
}

// fakeSession implements imapserver.Session with canned data.
type fakeSession struct {
	username      string
	password      string
	mailboxes     map[string][]fakeMessage
	selected      string
	authenticated bool
}

func (s *fakeSession) Close() error { return nil }

func (s *fakeSession) Login(username, password string) error {
	if username == s.username && password == s.password {
		s.authenticated = true
		return nil
	}
	return imapserver.ErrAuthFailed
}

func (s *fakeSession) Select(mailbox string, _ *imap.SelectOptions) (*imap.SelectData, error) {
	msgs, ok := s.mailboxes[mailbox]
	if !ok {
		return nil, fmt.Errorf("mailbox %q not found", mailbox)
	}
	s.selected = mailbox
	var maxUID imap.UID
	for _, m := range msgs {
		if m.uid > maxUID {
			maxUID = m.uid
		}
	}
	return &imap.SelectData{
		NumMessages: uint32(len(msgs)),
		UIDNext:     maxUID + 1,
		UIDValidity: 1,
	}, nil
}

func (s *fakeSession) Create(_ string, _ *imap.CreateOptions) error    { return nil }
func (s *fakeSession) Delete(_ string) error                           { return nil }
func (s *fakeSession) Rename(_, _ string, _ *imap.RenameOptions) error { return nil }
func (s *fakeSession) Subscribe(_ string) error                        { return nil }
func (s *fakeSession) Unsubscribe(_ string) error                      { return nil }

func (s *fakeSession) List(w *imapserver.ListWriter, _ string, _ []string, _ *imap.ListOptions) error {
	for name := range s.mailboxes {
		if err := w.WriteList(&imap.ListData{Mailbox: name, Delim: '/'}); err != nil {
			return err
		}
	}
	return nil
}

func (s *fakeSession) Status(mailbox string, _ *imap.StatusOptions) (*imap.StatusData, error) {
	msgs, ok := s.mailboxes[mailbox]
	if !ok {
		return nil, fmt.Errorf("mailbox %q not found", mailbox)
	}
	num := uint32(len(msgs))
	return &imap.StatusData{Mailbox: mailbox, NumMessages: &num}, nil
}

func (s *fakeSession) Append(_ string, r imap.LiteralReader, _ *imap.AppendOptions) (*imap.AppendData, error) {
	_, _ = io.ReadAll(r) //nolint:errcheck // drain the reader
	return &imap.AppendData{}, nil
}

func (s *fakeSession) Poll(_ *imapserver.UpdateWriter, _ bool) error { return nil }
func (s *fakeSession) Idle(_ *imapserver.UpdateWriter, stop <-chan struct{}) error {
	<-stop
	return nil
}
func (s *fakeSession) Unselect() error                                           { return nil }
func (s *fakeSession) Expunge(_ *imapserver.ExpungeWriter, _ *imap.UIDSet) error { return nil }

func (s *fakeSession) Search(_ imapserver.NumKind, criteria *imap.SearchCriteria, _ *imap.SearchOptions) (*imap.SearchData, error) {
	msgs := s.mailboxes[s.selected]
	var result imap.UIDSet

	for _, m := range msgs {
		if matchesCriteria(m, criteria) {
			result.AddNum(m.uid)
		}
	}

	return &imap.SearchData{All: result}, nil
}

func matchesCriteria(m fakeMessage, criteria *imap.SearchCriteria) bool {
	// Check UID filter
	if len(criteria.UID) > 0 {
		matched := false
		for _, uidSet := range criteria.UID {
			if uidSet.Contains(m.uid) {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	// Check Since filter
	if !criteria.Since.IsZero() {
		if m.date.Before(criteria.Since) {
			return false
		}
	}
	return true
}

func (s *fakeSession) Fetch(w *imapserver.FetchWriter, numSet imap.NumSet, options *imap.FetchOptions) error {
	msgs := s.mailboxes[s.selected]

	uidSet, ok := numSet.(imap.UIDSet)
	if !ok {
		return fmt.Errorf("expected UIDSet")
	}

	for i, m := range msgs {
		if !uidSet.Contains(m.uid) {
			continue
		}

		respWriter := w.CreateMessage(uint32(i + 1))

		if options.UID {
			respWriter.WriteUID(m.uid)
		}
		if options.Envelope && m.envelope != nil {
			respWriter.WriteEnvelope(m.envelope)
		}
		if len(options.BodySection) > 0 && len(m.body) > 0 {
			for _, section := range options.BodySection {
				bodyWriter := respWriter.WriteBodySection(section, int64(len(m.body)))
				_, _ = bodyWriter.Write(m.body)
				_ = bodyWriter.Close()
			}
		}

		if err := respWriter.Close(); err != nil {
			return err
		}
	}

	return nil
}

func (s *fakeSession) Store(_ *imapserver.FetchWriter, _ imap.NumSet, _ *imap.StoreFlags, _ *imap.StoreOptions) error {
	return nil
}

func (s *fakeSession) Copy(_ imap.NumSet, _ string) (*imap.CopyData, error) {
	return &imap.CopyData{}, nil
}

// startFakeIMAPServer starts a fake IMAP server and returns the address and a cleanup function.
func startFakeIMAPServer(messages map[string][]fakeMessage, username, password string) (string, func()) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		panic(err)
	}

	srv := imapserver.New(&imapserver.Options{
		NewSession: func(_ *imapserver.Conn) (imapserver.Session, *imapserver.GreetingData, error) {
			return &fakeSession{
				username:  username,
				password:  password,
				mailboxes: messages,
			}, nil, nil
		},
		InsecureAuth: true,
		Logger:       log.New(io.Discard, "", 0),
	})

	go func() {
		_ = srv.Serve(ln)
	}()

	return ln.Addr().String(), func() { _ = srv.Close() }
}

// buildTestEmail builds a raw email with the given plain text body.
func buildTestEmail(body string) []byte {
	var buf strings.Builder
	buf.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
	buf.WriteString("\r\n")
	buf.WriteString(body)
	return []byte(buf.String())
}
