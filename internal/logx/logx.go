package logx

import (
	"io"
	"log/slog"
	"os"
	"strings"
	"sync/atomic"
)

var global atomic.Value

func Init(level string, json bool, addSource bool, w io.Writer) {
	if w == nil {
		w = os.Stdout
	}

	var lv slog.Level
	switch strings.ToLower(level) {
	case "debug":
		lv = slog.LevelDebug
	case "warn":
		lv = slog.LevelWarn
	case "error":
		lv = slog.LevelError
	default:
		lv = slog.LevelInfo
	}
	opts := &slog.HandlerOptions{Level: lv, AddSource: addSource}
	var h slog.Handler
	if json {
		h = slog.NewJSONHandler(w, opts)
	} else {
		h = slog.NewTextHandler(w, opts)
	}
	global.Store(slog.New(h))
}

func base() *slog.Logger {
	if v := global.Load(); v != nil {
		return v.(*slog.Logger)
	}

	l := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	global.Store(l)
	return l
}

func Info(msg string, args ...any)  { base().Info(msg, args...) }
func Error(msg string, args ...any) { base().Error(msg, args...) }
func Debug(msg string, args ...any) { base().Debug(msg, args...) }

// With — get child logger
func With(args ...any) *slog.Logger { return base().With(args...) }

// Set — add base attribute (after welcome in transport)
func Set(args ...any) { global.Store(base().With(args...)) }
