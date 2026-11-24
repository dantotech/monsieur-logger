// Package applog configures the application's own (non-test) JSON logger.
//
// Application logs share the field names of the generated test logs
// (timestamp, level, message, app, kind) so that both can be ingested
// into the same ADX table. They are marked with kind="app".
package applog

import (
	"fmt"
	"io"
	"log/slog"
	"strings"
)

// AppName is the value of the "app" field on every log record.
const AppName = "monsieur-logger"

// ParseLevel converts debug|info|warn|warning|error into a slog.Level.
func ParseLevel(s string) (slog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return slog.LevelDebug, nil
	case "", "info":
		return slog.LevelInfo, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return slog.LevelInfo, fmt.Errorf("unknown log level %q (use debug, info, warn or error)", s)
	}
}

// New builds a JSON slog.Logger writing to w.
func New(w io.Writer, level slog.Level, static ...any) *slog.Logger {
	h := slog.NewJSONHandler(w, &slog.HandlerOptions{
		Level: level,
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			if len(groups) > 0 {
				return a
			}
			switch a.Key {
			case slog.TimeKey:
				a.Key = "timestamp"
				a.Value = slog.StringValue(a.Value.Time().UTC().Format("2006-01-02T15:04:05.000000000Z07:00"))
			case slog.LevelKey:
				a.Value = slog.StringValue(strings.ToLower(a.Value.Any().(slog.Level).String()))
			case slog.MessageKey:
				a.Key = "message"
			}
			return a
		},
	})
	attrs := append([]any{"app", AppName, "kind", "app"}, static...)
	return slog.New(h).With(attrs...)
}
