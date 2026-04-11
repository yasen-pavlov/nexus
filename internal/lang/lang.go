// Package lang defines the canonical list of languages Nexus analyzes.
//
// The same list drives OpenSearch text analysis (per-field language
// analyzers on title/content) and Tika OCR (X-Tika-OCRLanguage header)
// so a document's stemming and its underlying OCR agree on which
// languages to expect.
//
// Hardcoded today; when the Settings UI lands this becomes a DB-backed
// per-instance setting. Changes to the list will require a full reindex.
package lang

import "strings"

// Language describes one language Nexus indexes and OCRs.
type Language struct {
	// Name is the canonical lowercase identifier used as the OpenSearch
	// sub-field name (e.g. content.english) and as the primary key across
	// subsystems.
	Name string
	// OpenSearchAnalyzer is the name of an OpenSearch built-in language
	// analyzer. Feeding an unknown name to OpenSearch at index-create time
	// fails the whole boot, so values must be validated against Valid
	// before use.
	OpenSearchAnalyzer string
	// TesseractCode is the ISO 639-2/T three-letter code Tesseract uses,
	// e.g. "eng", "deu", "bul". Joined with "+" to form the
	// X-Tika-OCRLanguage header value.
	TesseractCode string
}

// Default returns the list of languages Nexus analyzes today. When the
// Settings UI is introduced this becomes a DB-backed setting; the single
// call site in cmd/nexus/main.go flips over without the consumers
// (search, extractor) needing to change.
func Default() []Language {
	return []Language{
		{Name: "english", OpenSearchAnalyzer: "english", TesseractCode: "eng"},
		{Name: "german", OpenSearchAnalyzer: "german", TesseractCode: "deu"},
		{Name: "bulgarian", OpenSearchAnalyzer: "bulgarian", TesseractCode: "bul"},
	}
}

// Valid is the whitelist of recognized canonical language names. It
// covers every OpenSearch built-in language analyzer that ships in core
// (no plugin required) and has a corresponding Tesseract language pack.
// Used to validate future user-supplied config from the Settings UI.
var Valid = map[string]Language{
	"arabic":     {Name: "arabic", OpenSearchAnalyzer: "arabic", TesseractCode: "ara"},
	"armenian":   {Name: "armenian", OpenSearchAnalyzer: "armenian", TesseractCode: "hye"},
	"basque":     {Name: "basque", OpenSearchAnalyzer: "basque", TesseractCode: "eus"},
	"bengali":    {Name: "bengali", OpenSearchAnalyzer: "bengali", TesseractCode: "ben"},
	"brazilian":  {Name: "brazilian", OpenSearchAnalyzer: "brazilian", TesseractCode: "por"},
	"bulgarian":  {Name: "bulgarian", OpenSearchAnalyzer: "bulgarian", TesseractCode: "bul"},
	"catalan":    {Name: "catalan", OpenSearchAnalyzer: "catalan", TesseractCode: "cat"},
	"czech":      {Name: "czech", OpenSearchAnalyzer: "czech", TesseractCode: "ces"},
	"danish":     {Name: "danish", OpenSearchAnalyzer: "danish", TesseractCode: "dan"},
	"dutch":      {Name: "dutch", OpenSearchAnalyzer: "dutch", TesseractCode: "nld"},
	"english":    {Name: "english", OpenSearchAnalyzer: "english", TesseractCode: "eng"},
	"estonian":   {Name: "estonian", OpenSearchAnalyzer: "estonian", TesseractCode: "est"},
	"finnish":    {Name: "finnish", OpenSearchAnalyzer: "finnish", TesseractCode: "fin"},
	"french":     {Name: "french", OpenSearchAnalyzer: "french", TesseractCode: "fra"},
	"galician":   {Name: "galician", OpenSearchAnalyzer: "galician", TesseractCode: "glg"},
	"german":     {Name: "german", OpenSearchAnalyzer: "german", TesseractCode: "deu"},
	"greek":      {Name: "greek", OpenSearchAnalyzer: "greek", TesseractCode: "ell"},
	"hindi":      {Name: "hindi", OpenSearchAnalyzer: "hindi", TesseractCode: "hin"},
	"hungarian":  {Name: "hungarian", OpenSearchAnalyzer: "hungarian", TesseractCode: "hun"},
	"indonesian": {Name: "indonesian", OpenSearchAnalyzer: "indonesian", TesseractCode: "ind"},
	"irish":      {Name: "irish", OpenSearchAnalyzer: "irish", TesseractCode: "gle"},
	"italian":    {Name: "italian", OpenSearchAnalyzer: "italian", TesseractCode: "ita"},
	"latvian":    {Name: "latvian", OpenSearchAnalyzer: "latvian", TesseractCode: "lav"},
	"lithuanian": {Name: "lithuanian", OpenSearchAnalyzer: "lithuanian", TesseractCode: "lit"},
	"norwegian":  {Name: "norwegian", OpenSearchAnalyzer: "norwegian", TesseractCode: "nor"},
	"persian":    {Name: "persian", OpenSearchAnalyzer: "persian", TesseractCode: "fas"},
	"portuguese": {Name: "portuguese", OpenSearchAnalyzer: "portuguese", TesseractCode: "por"},
	"romanian":   {Name: "romanian", OpenSearchAnalyzer: "romanian", TesseractCode: "ron"},
	"russian":    {Name: "russian", OpenSearchAnalyzer: "russian", TesseractCode: "rus"},
	"sorani":     {Name: "sorani", OpenSearchAnalyzer: "sorani", TesseractCode: "ckb"},
	"spanish":    {Name: "spanish", OpenSearchAnalyzer: "spanish", TesseractCode: "spa"},
	"swedish":    {Name: "swedish", OpenSearchAnalyzer: "swedish", TesseractCode: "swe"},
	"thai":       {Name: "thai", OpenSearchAnalyzer: "thai", TesseractCode: "tha"},
	"turkish":    {Name: "turkish", OpenSearchAnalyzer: "turkish", TesseractCode: "tur"},
}

// TesseractHeader returns the joined Tesseract code string for the given
// languages, suitable for use as the X-Tika-OCRLanguage header value.
// For Default() the result is "eng+deu+bul". Returns an empty string
// when the list is empty so callers can skip setting the header.
func TesseractHeader(ls []Language) string {
	if len(ls) == 0 {
		return ""
	}
	codes := make([]string, 0, len(ls))
	for _, l := range ls {
		if l.TesseractCode != "" {
			codes = append(codes, l.TesseractCode)
		}
	}
	return strings.Join(codes, "+")
}

// OpenSearchAnalyzers returns the analyzer names from the given list in
// the same order.
func OpenSearchAnalyzers(ls []Language) []string {
	out := make([]string, 0, len(ls))
	for _, l := range ls {
		out = append(out, l.OpenSearchAnalyzer)
	}
	return out
}
