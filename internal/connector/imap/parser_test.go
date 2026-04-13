package imap

import (
	"reflect"
	"testing"
)

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
