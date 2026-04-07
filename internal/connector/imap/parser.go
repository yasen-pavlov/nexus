package imap

import (
	"bytes"
	"io"
	"regexp"
	"strings"

	"github.com/emersion/go-message/mail"
)

// attachment represents an email attachment.
type attachment struct {
	Filename    string
	ContentType string
	Data        []byte
}

// parseEmailBody parses a raw email body (RFC 5322) and extracts the text content
// and attachments. It prefers text/plain over text/html for the body content.
func parseEmailBody(raw []byte) (string, []attachment) {
	if len(raw) == 0 {
		return "", nil
	}

	reader, err := mail.CreateReader(bytes.NewReader(raw))
	if err != nil {
		// Not a valid MIME message — treat the whole thing as plain text
		return string(raw), nil
	}
	defer reader.Close() //nolint:errcheck // best-effort close

	var plainText, htmlText string
	var attachments []attachment

	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			break
		}

		switch h := part.Header.(type) {
		case *mail.InlineHeader:
			contentType, _, _ := h.ContentType()

			body, readErr := io.ReadAll(part.Body)
			if readErr != nil {
				continue
			}

			switch {
			case strings.HasPrefix(contentType, "text/plain"):
				plainText = string(body)
			case strings.HasPrefix(contentType, "text/html") && htmlText == "":
				htmlText = string(body)
			}

		case *mail.AttachmentHeader:
			filename, _ := h.Filename()
			contentType, _, _ := h.ContentType()

			data, readErr := io.ReadAll(part.Body)
			if readErr != nil {
				continue
			}

			attachments = append(attachments, attachment{
				Filename:    filename,
				ContentType: contentType,
				Data:        data,
			})
		}
	}

	// Prefer plain text; fall back to stripped HTML
	content := plainText
	if content == "" && htmlText != "" {
		content = stripHTML(htmlText)
	}

	return strings.TrimSpace(content), attachments
}

var (
	htmlTagRe    = regexp.MustCompile(`<[^>]*>`)
	whitespaceRe = regexp.MustCompile(`\s{2,}`)
)

// stripHTML removes HTML tags and collapses whitespace.
func stripHTML(html string) string {
	text := htmlTagRe.ReplaceAllString(html, " ")
	text = whitespaceRe.ReplaceAllString(text, " ")
	return strings.TrimSpace(text)
}
