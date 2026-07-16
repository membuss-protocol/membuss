// Package logging is the Phase 10 structured-logging facade for
// Membuss. It wraps the standard library's log/slog with a
// Membuss-shaped constructor that maps a human-friendly level
// string ("debug", "info", "warn", "error") onto a slog.Level.
//
// Callers obtain a *slog.Logger via New and use it directly; no
// Membuss-specific wrappers are needed.
package logging

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"
)

// ParseLevel maps a level name to a slog.Level. Empty and
// unknown values fall back to LevelInfo.
func ParseLevel(s string) slog.Level {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.TrimSuffix(s, "-color")
	s = strings.TrimSuffix(s, "-json")
	switch s {
	case "debug":
		return slog.LevelDebug
	case "info", "":
		return slog.LevelInfo
	case "warn", "warning":
		return slog.LevelWarn
	case "error", "err":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// New returns a *slog.Logger that writes formatted log records to w.
// By default, it uses ColorHandler for human-friendly console output.
// If the levelStr ends with "-json", it falls back to standard JSON output.
func New(w io.Writer, levelStr string) *slog.Logger {
	if w == nil {
		w = io.Discard
	}
	level := ParseLevel(levelStr)
	var h slog.Handler
	if strings.HasSuffix(strings.ToLower(levelStr), "-json") {
		h = slog.NewJSONHandler(w, &slog.HandlerOptions{Level: level})
	} else {
		h = &ColorHandler{w: w, level: level}
	}
	return slog.New(&filterHandler{Handler: h}).With("membuss", "daemon")
}

// ColorHandler formats slog records with ANSI colors.
type ColorHandler struct {
	w     io.Writer
	level slog.Level
}

func (h *ColorHandler) Enabled(_ context.Context, l slog.Level) bool {
	return l >= h.level
}

func (h *ColorHandler) Handle(_ context.Context, r slog.Record) error {
	var levelStr string
	switch r.Level {
	case slog.LevelDebug:
		levelStr = "\x1b[36mDEBUG\x1b[0m" // Cyan
	case slog.LevelInfo:
		levelStr = "\x1b[32mINFO \x1b[0m" // Green
	case slog.LevelWarn:
		levelStr = "\x1b[33mWARN \x1b[0m" // Yellow
	case slog.LevelError:
		levelStr = "\x1b[31mERROR\x1b[0m" // Red
	default:
		levelStr = r.Level.String()
	}

	timeStr := r.Time.Format("15:04:05.000")
	fmt.Fprintf(h.w, "\x1b[90m%s\x1b[0m [%s] \x1b[1m%s\x1b[0m", timeStr, levelStr, r.Message)

	r.Attrs(func(a slog.Attr) bool {
		if a.Key == "membuss" && a.Value.String() == "daemon" {
			return true
		}
		fmt.Fprintf(h.w, " \x1b[36m%s\x1b[0m=%v", a.Key, a.Value.Any())
		return true
	})

	fmt.Fprintln(h.w)
	return nil
}

func (h *ColorHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return h
}

func (h *ColorHandler) WithGroup(name string) slog.Handler {
	return h
}

type filterHandler struct {
	slog.Handler
}

func (h *filterHandler) Handle(ctx context.Context, r slog.Record) error {
	if strings.Contains(r.Message, "Failed to set multicast interface") {
		return nil
	}
	return h.Handler.Handle(ctx, r)
}

func (h *filterHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &filterHandler{Handler: h.Handler.WithAttrs(attrs)}
}

func (h *filterHandler) WithGroup(name string) slog.Handler {
	return &filterHandler{Handler: h.Handler.WithGroup(name)}
}

// NewDiscard returns a logger that drops every record. Useful in
// tests that do not care about log output.
func NewDiscard() *slog.Logger {
	return slog.New(slog.NewJSONHandler(io.Discard, nil))
}
