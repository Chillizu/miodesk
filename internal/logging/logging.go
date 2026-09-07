// Package logging configures miodesk's structured process logs and carries a
// request identifier through HTTP/MCP contexts. The default sink is stderr so
// systemd user services are collected by journald without a second daemon.
package logging

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync/atomic"
	"time"
)

// Settings controls the process logger. Format is text or json; Level is one
// of debug, info, warn, or error.
type Settings struct {
	Level  string
	Format string
}

// Configure replaces the process-wide slog logger. A caller normally passes
// os.Stderr; systemd then stores the structured records in the user journal.
func Configure(settings Settings, w io.Writer) error {
	level, err := parseLevel(settings.Level)
	if err != nil {
		return err
	}
	format := strings.ToLower(strings.TrimSpace(settings.Format))
	if format == "" {
		format = "text"
	}
	if format != "text" && format != "json" {
		return fmt.Errorf("logging.format %q must be text or json", settings.Format)
	}
	if w == nil {
		w = os.Stderr
	}

	options := &slog.HandlerOptions{
		Level:       level,
		ReplaceAttr: replaceAttr,
	}
	var handler slog.Handler
	if format == "json" {
		handler = slog.NewJSONHandler(w, options)
	} else {
		handler = slog.NewTextHandler(w, options)
	}
	slog.SetDefault(slog.New(handler))
	return nil
}

func parseLevel(value string) (slog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "info":
		return slog.LevelInfo, nil
	case "debug":
		return slog.LevelDebug, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return slog.LevelInfo, fmt.Errorf("logging.level %q must be debug, info, warn, or error", value)
	}
}

// replaceAttr is defense in depth: code must never log secrets, but if a
// future call site supplies a credential-shaped field, slog redacts it at the
// final output boundary as well.
func replaceAttr(_ []string, attr slog.Attr) slog.Attr {
	switch strings.ToLower(attr.Key) {
	case "token", "access_token", "refresh_token", "authorization", "api_key", "control_plane_api_key", "client_secret", "secret":
		return slog.String(attr.Key, "[redacted]")
	default:
		return attr
	}
}

type requestIDKey struct{}

// WithRequestID attaches an externally generated request identifier to ctx.
func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requestIDKey{}, id)
}

// RequestID returns the request identifier carried by ctx, if any.
func RequestID(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	id, _ := ctx.Value(requestIDKey{}).(string)
	return id
}

var requestCounter atomic.Uint64

// NewRequestID returns a short, non-secret correlation identifier. The random
// component is preferred; a monotonic fallback keeps logging available even
// if the OS random source is temporarily unavailable.
func NewRequestID() string {
	var raw [8]byte
	if _, err := rand.Read(raw[:]); err == nil {
		return hex.EncodeToString(raw[:])
	}
	return fmt.Sprintf("%x-%x", time.Now().UnixNano(), requestCounter.Add(1))
}
