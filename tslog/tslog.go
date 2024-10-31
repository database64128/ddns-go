// Package tslog provides a tinted structured logging implementation.
package tslog

import (
	"context"
	"fmt"
	"log/slog"
	"net/netip"
	"os"
	"time"
	"unsafe"

	"github.com/lmittmann/tint"
)

// Logger is an opinionated logging implementation that writes structured log messages,
// tinted with color by default, to [os.Stderr].
type Logger struct {
	level   slog.Level
	noTime  bool
	handler slog.Handler
}

// New creates a new [*Logger] with the given options.
func New(level slog.Level, noColor, noTime bool) *Logger {
	handler := tint.NewHandler(os.Stderr, &tint.Options{
		Level:   level,
		NoColor: noColor,
	})
	return &Logger{level, noTime, handler}
}

// NewWithHandler creates a new [*Logger] with the given handler.
func NewWithHandler(level slog.Level, noTime bool, handler slog.Handler) *Logger {
	return &Logger{level, noTime, handler}
}

// Handler returns the logger's handler.
func (l *Logger) Handler() slog.Handler {
	return l.handler
}

// WithAttrs returns a new [*Logger] with the given attributes included in every log message.
func (l *Logger) WithAttrs(attrs ...slog.Attr) *Logger {
	return &Logger{
		level:   l.level,
		noTime:  l.noTime,
		handler: l.handler.WithAttrs(attrs),
	}
}

// WithGroup returns a new [*Logger] that scopes all log messages under the given group.
func (l *Logger) WithGroup(group string) *Logger {
	return &Logger{
		level:   l.level,
		noTime:  l.noTime,
		handler: l.handler.WithGroup(group),
	}
}

// Debug logs the given message at [slog.LevelDebug].
func (l *Logger) Debug(msg string, attrs ...slog.Attr) {
	l.Log(slog.LevelDebug, msg, attrs...)
}

// Info logs the given message at [slog.LevelInfo].
func (l *Logger) Info(msg string, attrs ...slog.Attr) {
	l.Log(slog.LevelInfo, msg, attrs...)
}

// Warn logs the given message at [slog.LevelWarn].
func (l *Logger) Warn(msg string, attrs ...slog.Attr) {
	l.Log(slog.LevelWarn, msg, attrs...)
}

// Error logs the given message at [slog.LevelError].
func (l *Logger) Error(msg string, attrs ...slog.Attr) {
	l.Log(slog.LevelError, msg, attrs...)
}

// Enabled returns whether logging at the given level is enabled.
func (l *Logger) Enabled(level slog.Level) bool {
	return level >= l.level
}

// Log logs the given message at the given level.
func (l *Logger) Log(level slog.Level, msg string, attrs ...slog.Attr) {
	if !l.Enabled(level) {
		return
	}
	l.log(level, msg, attrs...)
}

// log implements the actual logging logic, so that its callers (the exported log methods)
// become eligible for mid-stack inlining.
func (l *Logger) log(level slog.Level, msg string, attrs ...slog.Attr) {
	var t time.Time
	if !l.noTime {
		t = time.Now()
	}
	r := slog.NewRecord(t, level, msg, 0)
	r.AddAttrs(attrs...)
	if err := l.handler.Handle(context.Background(), r); err != nil {
		fmt.Fprintf(os.Stderr, "tslog: failed to write log message: %v\n", err)
	}
}

// Err is a convenience wrapper for [tint.Err].
func Err(err error) slog.Attr {
	return tint.Err(err)
}

// Int returns a [slog.Attr] for a signed integer of any size.
func Int[V ~int | ~int8 | ~int16 | ~int32 | ~int64](key string, value V) slog.Attr {
	return slog.Int64(key, int64(value))
}

// Uint returns a [slog.Attr] for an unsigned integer of any size.
func Uint[V ~uint | ~uint8 | ~uint16 | ~uint32 | ~uint64 | ~uintptr](key string, value V) slog.Attr {
	return slog.Uint64(key, uint64(value))
}

// Addr returns a [slog.Attr] for a [netip.Addr].
func Addr(key string, addr netip.Addr) slog.Attr {
	b, _ := addr.MarshalText()
	s := unsafe.String(unsafe.SliceData(b), len(b))
	return slog.String(key, s)
}

// AddrPort returns a [slog.Attr] for a [netip.AddrPort].
func AddrPort(key string, addrPort netip.AddrPort) slog.Attr {
	b, _ := addrPort.MarshalText()
	s := unsafe.String(unsafe.SliceData(b), len(b))
	return slog.String(key, s)
}

// Addrp returns a [slog.Attr] for a [*netip.Addr].
//
// Use [Addr] if the address is not already on the heap,
// or the call is guarded by [Logger.Enabled].
func Addrp(key string, addrp *netip.Addr) slog.Attr {
	return slog.Any(key, addrp)
}

// AddrPortp returns a [slog.Attr] for a [*netip.AddrPort].
//
// Use [AddrPort] if the address is not already on the heap,
// or the call is guarded by [Logger.Enabled].
func AddrPortp(key string, addrPortp *netip.AddrPort) slog.Attr {
	return slog.Any(key, addrPortp)
}
