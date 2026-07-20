package imap

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestParseEmailBody_ConcatenatesMultipleTextPlainParts(t *testing.T) {
	// A multipart/mixed message with two inline text/plain parts (e.g. the
	// body plus an inline forwarded message). Both must survive — the old
	// last-wins behavior silently dropped the first part from the index.
	boundary := "B0UND"
	var buf strings.Builder
	fmt.Fprintf(&buf, "Content-Type: multipart/mixed; boundary=%s\r\n\r\n", boundary)
	buf.WriteString("--" + boundary + "\r\n")
	buf.WriteString("Content-Type: text/plain; charset=utf-8\r\n\r\n")
	buf.WriteString("ORIGINAL BODY\r\n")
	buf.WriteString("--" + boundary + "\r\n")
	buf.WriteString("Content-Type: text/plain; charset=utf-8\r\n\r\n")
	buf.WriteString("FORWARDED PART\r\n")
	buf.WriteString("--" + boundary + "--\r\n")

	content, _ := parseEmailBody([]byte(buf.String()))
	if !strings.Contains(content, "ORIGINAL BODY") {
		t.Errorf("lost the original body part: %q", content)
	}
	if !strings.Contains(content, "FORWARDED PART") {
		t.Errorf("lost the forwarded part: %q", content)
	}
}

func TestParseEmailBody_InlineNonTextTreatedAsAttachment(t *testing.T) {
	// Apple Mail/iOS attaches photos and PDFs with Content-Disposition: inline;
	// go-message classifies any inline part as InlineHeader. Non-text inline
	// parts (or inline parts with a filename) must become attachments, not be
	// silently dropped.
	boundary := "B0UND"
	var buf strings.Builder
	fmt.Fprintf(&buf, "Content-Type: multipart/mixed; boundary=%s\r\n\r\n", boundary)
	// 1. Plain-text inline body, no filename → stays body.
	buf.WriteString("--" + boundary + "\r\n")
	buf.WriteString("Content-Type: text/plain; charset=utf-8\r\n\r\n")
	buf.WriteString("VISIBLE BODY\r\n")
	// 2. Inline PNG with a filename → attachment.
	buf.WriteString("--" + boundary + "\r\n")
	buf.WriteString("Content-Type: image/png\r\n")
	buf.WriteString("Content-Disposition: inline; filename=\"photo.png\"\r\n\r\n")
	buf.WriteString("PNGDATA\r\n")
	// 3. Inline PDF named via the Content-Type name param → attachment.
	buf.WriteString("--" + boundary + "\r\n")
	buf.WriteString("Content-Type: application/pdf; name=\"doc.pdf\"\r\n")
	buf.WriteString("Content-Disposition: inline\r\n\r\n")
	buf.WriteString("PDFDATA\r\n")
	buf.WriteString("--" + boundary + "--\r\n")

	content, attachments := parseEmailBody([]byte(buf.String()))
	if !strings.Contains(content, "VISIBLE BODY") {
		t.Errorf("lost the inline body text: %q", content)
	}
	if strings.Contains(content, "PNGDATA") || strings.Contains(content, "PDFDATA") {
		t.Errorf("attachment bytes leaked into body text: %q", content)
	}
	if len(attachments) != 2 {
		t.Fatalf("expected 2 inline attachments, got %d (%+v)", len(attachments), attachments)
	}
	byName := map[string]attachment{}
	for _, a := range attachments {
		byName[a.Filename] = a
	}
	png, ok := byName["photo.png"]
	if !ok || png.ContentType != "image/png" || len(png.Data) == 0 {
		t.Errorf("photo.png attachment wrong: %+v", png)
	}
	pdf, ok := byName["doc.pdf"]
	if !ok || pdf.ContentType != "application/pdf" || len(pdf.Data) == 0 {
		t.Errorf("doc.pdf attachment wrong: %+v", pdf)
	}
}

func TestDecodeHeader(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "plain ASCII passthrough",
			in:   "Hello world",
			want: "Hello world",
		},
		{
			name: "empty string",
			in:   "",
			want: "",
		},
		{
			name: "UTF-8 Q-encoded",
			in:   "=?UTF-8?Q?Gr=C3=BC=C3=9Fe?=",
			want: "Grüße",
		},
		{
			name: "UTF-8 base64",
			in:   "=?UTF-8?B?R3LDvMOfZQ==?=",
			want: "Grüße",
		},
		{
			name: "windows-1252 Q-encoded (needs emersion charset reader)",
			in:   "=?windows-1252?Q?Unser_Anspruch_an_die_Windows-Qualit=E4t?=",
			want: "Unser Anspruch an die Windows-Qualität",
		},
		{
			name: "mixed encoded + raw",
			in:   "Re: =?UTF-8?Q?Gr=C3=BC=C3=9Fe?= aus dem Büro",
			want: "Re: Grüße aus dem Büro",
		},
		{
			name: "malformed input falls back to raw",
			in:   "=?not-a-charset?Q?zzz?=",
			want: "=?not-a-charset?Q?zzz?=",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := decodeHeader(tt.in)
			if got != tt.want {
				t.Errorf("decodeHeader(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestParseReferencesHeader(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want []string
	}{
		{
			name: "empty body",
			raw:  "",
			want: nil,
		},
		{
			name: "no References header",
			raw:  "From: a@b.com\r\nSubject: Hi\r\n\r\nbody\r\n",
			want: nil,
		},
		{
			name: "single reference stripped of brackets",
			raw:  "References: <msg-1@x>\r\n\r\nbody",
			want: []string{"msg-1@x"},
		},
		{
			name: "multiple references, whitespace-separated",
			raw:  "References: <a@x> <b@x>\t<c@x>\r\n\r\nbody",
			want: []string{"a@x", "b@x", "c@x"},
		},
		{
			name: "folded header (continuation line)",
			raw:  "References: <a@x>\r\n <b@x>\r\n\r\nbody",
			want: []string{"a@x", "b@x"},
		},
		{
			name: "malformed body — not RFC 5322",
			raw:  "this is not an email",
			want: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseReferencesHeader([]byte(tt.raw))
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got %#v, want %#v", got, tt.want)
			}
		})
	}
}
