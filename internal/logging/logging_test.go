package logging

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
)

func TestNewHandler_LevelFiltering(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(newHandler(&buf, "warn", "json"))

	logger.Info("should be filtered")
	if buf.Len() != 0 {
		t.Fatalf("expected info log to be filtered at warn level, got %q", buf.String())
	}

	logger.Warn("should pass")
	if buf.Len() == 0 {
		t.Fatal("expected warn log to pass at warn level, got nothing")
	}
}

func TestNewHandler_Format(t *testing.T) {
	tests := []struct {
		name   string
		format string
		check  func(t *testing.T, line string)
	}{
		{
			name:   "json",
			format: "json",
			check: func(t *testing.T, line string) {
				var m map[string]any
				if err := json.Unmarshal([]byte(line), &m); err != nil {
					t.Fatalf("expected valid JSON, got %q: %v", line, err)
				}
			},
		},
		{
			name:   "text",
			format: "text",
			check: func(t *testing.T, line string) {
				if json.Valid([]byte(line)) {
					t.Fatalf("expected non-JSON text output, got %q", line)
				}
				if !strings.Contains(line, "msg=") {
					t.Fatalf("expected slog text handler output, got %q", line)
				}
			},
		},
		{
			name:   "unrecognized format defaults to json",
			format: "yaml",
			check: func(t *testing.T, line string) {
				var m map[string]any
				if err := json.Unmarshal([]byte(line), &m); err != nil {
					t.Fatalf("expected valid JSON, got %q: %v", line, err)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			logger := slog.New(newHandler(&buf, "info", tt.format))
			logger.Info("hello")
			tt.check(t, buf.String())
		})
	}
}

func TestNewHandler_UnrecognizedLevelDefaultsToInfo(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(newHandler(&buf, "verbose", "json"))

	logger.Debug("should be filtered")
	if buf.Len() != 0 {
		t.Fatalf("expected debug log to be filtered at default info level, got %q", buf.String())
	}

	logger.Info("should pass")
	if buf.Len() == 0 {
		t.Fatal("expected info log to pass at default info level, got nothing")
	}
}
