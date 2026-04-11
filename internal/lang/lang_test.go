package lang

import (
	"testing"
)

func TestDefaultEntriesInValid(t *testing.T) {
	for _, l := range Default() {
		got, ok := Valid[l.Name]
		if !ok {
			t.Errorf("Default() entry %q missing from Valid", l.Name)
			continue
		}
		if got != l {
			t.Errorf("Default() entry %q does not match Valid: got %+v want %+v", l.Name, l, got)
		}
	}
}

func TestDefaultNonEmpty(t *testing.T) {
	ls := Default()
	if len(ls) == 0 {
		t.Fatal("Default() returned empty slice")
	}
	for _, l := range ls {
		if l.Name == "" || l.OpenSearchAnalyzer == "" || l.TesseractCode == "" {
			t.Errorf("Default() entry has empty field: %+v", l)
		}
	}
}

func TestValidAllFieldsPresent(t *testing.T) {
	for name, l := range Valid {
		if l.Name != name {
			t.Errorf("Valid[%q].Name = %q, want %q", name, l.Name, name)
		}
		if l.OpenSearchAnalyzer == "" {
			t.Errorf("Valid[%q].OpenSearchAnalyzer is empty", name)
		}
		if l.TesseractCode == "" {
			t.Errorf("Valid[%q].TesseractCode is empty", name)
		}
	}
}

func TestTesseractHeader(t *testing.T) {
	tests := []struct {
		name string
		in   []Language
		want string
	}{
		{"default", Default(), "eng+deu+bul"},
		{"empty", nil, ""},
		{"single", []Language{{TesseractCode: "eng"}}, "eng"},
		{
			"two",
			[]Language{{TesseractCode: "eng"}, {TesseractCode: "fra"}},
			"eng+fra",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := TesseractHeader(tt.in); got != tt.want {
				t.Errorf("TesseractHeader() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestOpenSearchAnalyzers(t *testing.T) {
	got := OpenSearchAnalyzers(Default())
	want := []string{"english", "german", "bulgarian"}
	if len(got) != len(want) {
		t.Fatalf("OpenSearchAnalyzers() len = %d, want %d", len(got), len(want))
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("OpenSearchAnalyzers()[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}
