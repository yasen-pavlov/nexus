package imap

import (
	"reflect"
	"testing"
)

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
