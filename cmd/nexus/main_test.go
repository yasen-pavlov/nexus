package main

import (
	"testing"

	"go.uber.org/zap/zapcore"
)

// TestNewLogger_AppliesLevel proves NEXUS_LOG_LEVEL values beyond "debug"
// actually change the logger's enabled level, and that empty/unrecognised
// values fall back to info rather than being silently ignored.
func TestNewLogger_AppliesLevel(t *testing.T) {
	cases := []struct {
		level        string
		enabledDebug bool
		enabledInfo  bool
		enabledWarn  bool
		enabledError bool
	}{
		{"debug", true, true, true, true},
		{"info", false, true, true, true},
		{"warn", false, false, true, true},
		{"error", false, false, false, true},
		{"", false, true, true, true},      // empty → production default (info)
		{"bogus", false, true, true, true}, // unrecognised → fallback info
	}
	for _, tc := range cases {
		t.Run(tc.level, func(t *testing.T) {
			log := newLogger(tc.level)
			if log == nil {
				t.Fatal("newLogger returned nil")
			}
			core := log.Core()
			if got := core.Enabled(zapcore.DebugLevel); got != tc.enabledDebug {
				t.Errorf("debug enabled = %v, want %v", got, tc.enabledDebug)
			}
			if got := core.Enabled(zapcore.InfoLevel); got != tc.enabledInfo {
				t.Errorf("info enabled = %v, want %v", got, tc.enabledInfo)
			}
			if got := core.Enabled(zapcore.WarnLevel); got != tc.enabledWarn {
				t.Errorf("warn enabled = %v, want %v", got, tc.enabledWarn)
			}
			if got := core.Enabled(zapcore.ErrorLevel); got != tc.enabledError {
				t.Errorf("error enabled = %v, want %v", got, tc.enabledError)
			}
		})
	}
}
