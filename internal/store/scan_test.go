package store

import (
	"errors"
	"testing"
)

// fakeRows is a minimal Next/Scan/Err implementation that feeds
// scripted outcomes to scanBinaryStoreEntries. Pure unit test — no
// DB required — covers the function's three branches (scan error,
// rows.Err, happy path iteration).
type fakeRows struct {
	idx       int
	scans     []func(dest ...any) error
	iterErr   error
	returnEnd int
}

func (f *fakeRows) Next() bool {
	ok := f.idx < f.returnEnd
	if ok {
		f.idx++
	}
	return ok
}

func (f *fakeRows) Scan(dest ...any) error {
	return f.scans[f.idx-1](dest...)
}

func (f *fakeRows) Err() error {
	return f.iterErr
}

// TestScanBinaryStoreEntries_ScanError covers the per-row error
// path — a broken row short-circuits the iteration and wraps the
// underlying error with the store-layer prefix.
func TestScanBinaryStoreEntries_ScanError(t *testing.T) {
	rows := &fakeRows{
		returnEnd: 1,
		scans: []func(...any) error{
			func(...any) error { return errors.New("column type mismatch") },
		},
	}
	_, err := scanBinaryStoreEntries(rows)
	if err == nil || !contains(err.Error(), "scan binary store entry") {
		t.Errorf("expected wrapped scan error, got %v", err)
	}
}

// TestScanBinaryStoreEntries_IterErr covers the terminal rows.Err
// path — an iteration error after a successful row must surface.
func TestScanBinaryStoreEntries_IterErr(t *testing.T) {
	rows := &fakeRows{
		returnEnd: 0, // zero rows so Scan is never called
		iterErr:   errors.New("connection reset"),
	}
	_, err := scanBinaryStoreEntries(rows)
	if err == nil || !contains(err.Error(), "iterate binary store entries") {
		t.Errorf("expected wrapped iter error, got %v", err)
	}
}

// TestScanBinaryStoreEntries_EmptyReturnsNil covers the zero-rows
// happy path.
func TestScanBinaryStoreEntries_EmptyReturnsNil(t *testing.T) {
	rows := &fakeRows{returnEnd: 0}
	got, err := scanBinaryStoreEntries(rows)
	if err != nil {
		t.Errorf("unexpected err: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty slice, got %d entries", len(got))
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
