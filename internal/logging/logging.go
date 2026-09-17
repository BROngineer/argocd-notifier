package logging

import (
	"io"
	"log/slog"
	"os"
	"strings"
)

func New(level, format string) *slog.Logger {
	return slog.New(newHandler(os.Stdout, level, format))
}

func newHandler(w io.Writer, level, format string) slog.Handler {
	var lvl slog.Level
	switch strings.ToLower(level) {
	case "debug":
		lvl = slog.LevelDebug
	case "warn", "warning":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}

	opts := &slog.HandlerOptions{Level: lvl}

	if strings.ToLower(format) == "text" {
		return slog.NewTextHandler(w, opts)
	}
	return slog.NewJSONHandler(w, opts)
}
