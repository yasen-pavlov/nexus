package imap

import (
	"bytes"
	"io"
	"mime"
	netmail "net/mail"
	"regexp"
	"strings"

	"github.com/emersion/go-message/charset"
	"github.com/emersion/go-message/mail"
	"golang.org/x/net/html"
)

// headerDecoder decodes RFC 2047 encoded-word headers (e.g.
// `=?windows-1252?Q?Unser_Anspruch_an_die_Windows-Qualit=E4t?=` → German
// text with the umlaut). The IMAP Envelope fields come through raw —
// Go's mime.WordDecoder handles the standard charsets but doesn't know
// windows-1252, koi8-r, etc. on its own, so we wire in emersion's
// charset reader which registers ~every IANA-listed encoding.
var headerDecoder = mime.WordDecoder{CharsetReader: charset.Reader}

// decodeHeader returns the UTF-8 form of an RFC 2047 encoded header
// value. Falls back to the raw input on any decoder error — a
// malformed header should surface as the raw bytes in the UI rather
// than disappearing or failing the whole email.
func decodeHeader(s string) string {
	if s == "" || !strings.Contains(s, "=?") {
		return s
	}
	out, err := headerDecoder.DecodeHeader(s)
	if err != nil {
		return s
	}
	return out
}

// parseReferencesHeader pulls the `References:` header out of a raw RFC 5322
// message body and returns the Message-ID list as a slice with surrounding
// angle brackets stripped. Returns nil when the header is absent or the body
// isn't parseable — both are acceptable (the email just has no thread signal).
//
// We go through net/mail rather than emersion/go-message/mail because emersion's
// mail.Reader only exposes part headers, not the top-level message headers.
func parseReferencesHeader(raw []byte) []string {
	if len(raw) == 0 {
		return nil
	}
	msg, err := netmail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		return nil
	}
	header := msg.Header.Get("References")
	if header == "" {
		return nil
	}
	var out []string
	for _, tok := range strings.Fields(header) {
		tok = strings.TrimSpace(tok)
		tok = strings.TrimPrefix(tok, "<")
		tok = strings.TrimSuffix(tok, ">")
		if tok != "" {
			out = append(out, tok)
		}
	}
	return out
}

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

	// Email-specific cleaning: strip tracking URLs, quoted replies, signatures.
	// This applies to both the plain-text and HTML-stripped paths because plain
	// text emails also contain trackers, signatures, etc.
	content = cleanEmailText(content)

	return strings.TrimSpace(content), attachments
}

// stripHTML walks the HTML DOM and extracts the user-visible text. It drops
// entire <style>, <script>, <head>, and <noscript> subtrees (which would
// otherwise contribute massive amounts of CSS / JS / metadata noise to the
// embedding) and unwraps <a> elements (keeping the link text but dropping the
// href, which in marketing emails is almost always a base64-encoded tracking
// redirect that has no semantic value).
func stripHTML(htmlContent string) string {
	doc, err := html.Parse(strings.NewReader(htmlContent))
	if err != nil {
		// Parsing failed (shouldn't happen — html.Parse is permissive) — fall
		// back to the old regex stripper so we still extract something.
		return stripHTMLRegex(htmlContent)
	}

	var sb strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			switch n.Data {
			case "style", "script", "head", "noscript":
				return // skip entire subtree
			case "br":
				sb.WriteByte('\n')
			case "p", "div", "li", "tr", "h1", "h2", "h3", "h4", "h5", "h6":
				sb.WriteByte('\n')
			}
		}
		if n.Type == html.TextNode {
			sb.WriteString(n.Data)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
		// Add a trailing newline after block-level elements so paragraphs
		// don't get smashed together.
		if n.Type == html.ElementNode {
			switch n.Data {
			case "p", "div", "li", "tr", "h1", "h2", "h3", "h4", "h5", "h6":
				sb.WriteByte('\n')
			}
		}
	}
	walk(doc)

	text := sb.String()
	text = whitespaceRe.ReplaceAllString(text, " ")
	return strings.TrimSpace(text)
}

// stripHTMLRegex is a fallback for when html.Parse somehow fails. It's the
// original naive regex implementation — kept only as a safety net.
func stripHTMLRegex(htmlContent string) string {
	text := htmlTagRe.ReplaceAllString(htmlContent, " ")
	text = whitespaceRe.ReplaceAllString(text, " ")
	return strings.TrimSpace(text)
}

var (
	htmlTagRe    = regexp.MustCompile(`<[^>]*>`)
	whitespaceRe = regexp.MustCompile(`[ \t]{2,}`)

	// trackingURLRe matches common tracking-link domain patterns. Many marketing
	// emails route every link through a redirector with a base64-encoded path,
	// and those URLs end up in chunks where they dominate the content.
	trackingURLRe = regexp.MustCompile(`https?://[^\s<>()]*(?:track|click|email|mc|sg|sl|t\.co|list-manage|sendgrid|mailgun|sparkpost|mandrillapp|cmail|mailchi|hubspot|salesforce|pardot|marketo)[^\s<>()]*`)

	// longURLRe matches any URL whose path/query contains a 60+ char opaque
	// blob — base64, hashes, JWT-like tokens. This catches the long-tail of
	// tracking links that don't match a known domain. The character class
	// includes `=` (base64 padding, query param separators) and `&`.
	longURLRe = regexp.MustCompile(`https?://[^\s<>()"]*[A-Za-z0-9_/+%=&-]{60,}[^\s<>()"]*`)

	// quotedReplyHeaderRes matches the "On <date>, <person> wrote:" line
	// (and locale-specific variants) that precedes quoted reply blocks in
	// most clients. These run independently of the configured index
	// languages — a user may receive a German quoted reply even if their
	// instance indexes only English, and stripping it is always correct.
	quotedReplyHeaderRes = []*regexp.Regexp{
		regexp.MustCompile(`(?m)^On .+ wrote:.*$`),                  // English
		regexp.MustCompile(`(?m)^Am .+ schrieb .+:.*$`),             // German
		regexp.MustCompile(`(?m)^Le .+ a écrit\s*:.*$`),             // French
		regexp.MustCompile(`(?m)^El .+ escribió:.*$`),               // Spanish
		regexp.MustCompile(`(?m)^Il .+ ha scritto:.*$`),             // Italian
		regexp.MustCompile(`(?m)^Em .+ escreveu:.*$`),               // Portuguese
		regexp.MustCompile(`(?m)^Op .+ schreef .+:.*$`),             // Dutch
		regexp.MustCompile(`(?m)^На .+ (?:в|написа|написал).*:.*$`), // Bulgarian
	}

	// blankLineRe collapses 3+ consecutive newlines into two.
	blankLineRe = regexp.MustCompile(`\n{3,}`)
)

// cleanEmailText applies email-specific text cleaning to remove tracking URLs,
// quoted reply blocks, and signature blocks. It runs on both the plain-text
// and HTML-stripped paths in parseEmailBody — both contain this kind of noise.
func cleanEmailText(text string) string {
	if text == "" {
		return ""
	}

	// Strip tracking URLs (specific domains) first, then long opaque URLs.
	text = trackingURLRe.ReplaceAllString(text, "")
	text = longURLRe.ReplaceAllString(text, "")

	// Remove the "On <date>, <person> wrote:" header (and locale variants)
	// that introduces quoted replies. The actual quoted lines (starting
	// with `>`) get stripped below.
	for _, re := range quotedReplyHeaderRes {
		text = re.ReplaceAllString(text, "")
	}

	// Walk lines: drop quoted-reply lines (starting with `>`), and stop at the
	// signature delimiter (`-- ` on its own line — RFC 3676 section 4.3).
	var out []string
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimRight(line, " \t")
		if trimmed == "-- " || trimmed == "--" {
			break // signature block — drop everything from here on
		}
		stripped := strings.TrimLeft(trimmed, " \t")
		if strings.HasPrefix(stripped, ">") {
			continue // quoted reply line
		}
		out = append(out, trimmed)
	}
	text = strings.Join(out, "\n")

	// Collapse repeated blank lines and intra-line whitespace.
	text = blankLineRe.ReplaceAllString(text, "\n\n")
	text = whitespaceRe.ReplaceAllString(text, " ")

	return strings.TrimSpace(text)
}
