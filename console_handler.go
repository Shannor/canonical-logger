package clog

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	ansiReset   = "\x1b[0m"
	ansiDim     = "\x1b[2m"
	ansiBold    = "\x1b[1m"
	ansiRed     = "\x1b[31m"
	ansiGreen   = "\x1b[32m"
	ansiYellow  = "\x1b[33m"
	ansiBlue    = "\x1b[34m"
	ansiMagenta = "\x1b[35m"
	ansiCyan    = "\x1b[36m"
)

// ConsoleHandlerOptions configures ConsoleHandler behavior.
type ConsoleHandlerOptions struct {
	// Level reports the minimum record level that will be logged.
	// If nil, slog.LevelInfo is used.
	Level slog.Leveler

	// NoColor disables ANSI color escape sequences when true.
	NoColor bool

	// TimeFormat specifies the time layout for timestamps.
	// Defaults to "15:04:05.000" if empty.
	TimeFormat string
}

// ConsoleHandler is a human-readable, optionally colorized slog.Handler
// modeled after zerolog.ConsoleWriter for local development.
type ConsoleHandler struct {
	opts        ConsoleHandlerOptions
	w           io.Writer
	mu          *sync.Mutex
	preformatted []slog.Attr
	groups      []string
}

// NewConsoleHandler creates a new ConsoleHandler writing to w.
func NewConsoleHandler(w io.Writer, opts *ConsoleHandlerOptions) *ConsoleHandler {
	var o ConsoleHandlerOptions
	if opts != nil {
		o = *opts
	}
	if o.TimeFormat == "" {
		o.TimeFormat = "15:04:05.000"
	}
	return &ConsoleHandler{
		opts: o,
		w:    w,
		mu:   &sync.Mutex{},
	}
}

// Enabled implements slog.Handler.Enabled.
func (h *ConsoleHandler) Enabled(_ context.Context, level slog.Level) bool {
	minLevel := slog.LevelInfo
	if h.opts.Level != nil {
		minLevel = h.opts.Level.Level()
	}
	return level >= minLevel
}

// Handle implements slog.Handler.Handle.
func (h *ConsoleHandler) Handle(_ context.Context, r slog.Record) error {
	var buf bytes.Buffer

	if !r.Time.IsZero() {
		ts := r.Time.Format(h.opts.TimeFormat)
		h.writeColored(&buf, ansiDim, ts)
		buf.WriteByte(' ')
	}

	lvlLabel, lvlColor := formatConsoleLevel(r.Level)
	h.writeColored(&buf, lvlColor, lvlLabel)
	buf.WriteByte(' ')

	h.writeColored(&buf, ansiBold, r.Message)

	allAttrs := make([]slog.Attr, 0, len(h.preformatted)+r.NumAttrs())
	allAttrs = append(allAttrs, h.preformatted...)

	var recordAttrs []slog.Attr
	r.Attrs(func(a slog.Attr) bool {
		recordAttrs = append(recordAttrs, a)
		return true
	})
	if len(recordAttrs) > 0 {
		for i := len(h.groups) - 1; i >= 0; i-- {
			recordAttrs = []slog.Attr{slog.GroupAttrs(h.groups[i], recordAttrs...)}
		}
		allAttrs = append(allAttrs, recordAttrs...)
	}

	for _, a := range allAttrs {
		h.formatAttr(&buf, "", a)
	}

	buf.WriteByte('\n')

	h.mu.Lock()
	defer h.mu.Unlock()
	_, err := h.w.Write(buf.Bytes())
	return err
}

// WithAttrs implements slog.Handler.WithAttrs.
func (h *ConsoleHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	if len(attrs) == 0 {
		return h
	}
	wrapped := slices.Clone(attrs)
	for i := len(h.groups) - 1; i >= 0; i-- {
		wrapped = []slog.Attr{slog.GroupAttrs(h.groups[i], wrapped...)}
	}
	pre := make([]slog.Attr, 0, len(h.preformatted)+len(wrapped))
	pre = append(pre, h.preformatted...)
	pre = append(pre, wrapped...)
	return &ConsoleHandler{
		opts:         h.opts,
		w:            h.w,
		mu:           h.mu,
		preformatted: pre,
		groups:       slices.Clone(h.groups),
	}
}

// WithGroup implements slog.Handler.WithGroup.
func (h *ConsoleHandler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	groups := make([]string, 0, len(h.groups)+1)
	groups = append(groups, h.groups...)
	groups = append(groups, name)
	return &ConsoleHandler{
		opts:         h.opts,
		w:            h.w,
		mu:           h.mu,
		preformatted: slices.Clone(h.preformatted),
		groups:       groups,
	}
}

func (h *ConsoleHandler) formatAttr(buf *bytes.Buffer, prefix string, a slog.Attr) {
	a.Value = a.Value.Resolve()
	if a.Equal(slog.Attr{}) {
		return
	}

	key := a.Key
	if prefix != "" && key != "" {
		key = prefix + "." + key
	} else if prefix != "" {
		key = prefix
	}

	if a.Value.Kind() == slog.KindGroup {
		groupAttrs := a.Value.Group()
		if len(groupAttrs) == 0 {
			return
		}
		buf.WriteByte(' ')
		keyColor := ansiCyan
		if key == "error" || strings.HasPrefix(key, "error.") {
			keyColor = ansiRed
		}
		h.writeColored(buf, keyColor, key)
		buf.WriteString("={")
		for i, ga := range groupAttrs {
			if i > 0 {
				buf.WriteByte(' ')
			}
			h.formatGroupMember(buf, key == "error", ga)
		}
		buf.WriteByte('}')
		return
	}

	buf.WriteByte(' ')
	keyColor := ansiCyan
	if key == "error" || key == "err" || key == "gin_error" {
		keyColor = ansiRed
	}
	h.writeColored(buf, keyColor, key)
	buf.WriteByte('=')
	h.writeValue(buf, keyColor == ansiRed, a.Value)
}

func (h *ConsoleHandler) formatGroupMember(buf *bytes.Buffer, isErrGroup bool, a slog.Attr) {
	a.Value = a.Value.Resolve()
	if a.Equal(slog.Attr{}) {
		return
	}
	if a.Value.Kind() == slog.KindGroup {
		groupAttrs := a.Value.Group()
		keyColor := ansiCyan
		if isErrGroup || a.Key == "error" {
			keyColor = ansiRed
		}
		h.writeColored(buf, keyColor, a.Key)
		buf.WriteString("={")
		for i, ga := range groupAttrs {
			if i > 0 {
				buf.WriteByte(' ')
			}
			h.formatGroupMember(buf, isErrGroup || a.Key == "error", ga)
		}
		buf.WriteByte('}')
		return
	}

	keyColor := ansiCyan
	if isErrGroup || a.Key == "err" || a.Key == "error" {
		keyColor = ansiRed
	}
	h.writeColored(buf, keyColor, a.Key)
	buf.WriteByte('=')
	h.writeValue(buf, isErrGroup, a.Value)
}

func (h *ConsoleHandler) writeValue(buf *bytes.Buffer, isError bool, v slog.Value) {
	str := formatSlogValue(v)
	if isError {
		h.writeColored(buf, ansiRed, str)
		return
	}
	buf.WriteString(str)
}

func formatSlogValue(v slog.Value) string {
	switch v.Kind() {
	case slog.KindString:
		s := v.String()
		if needsQuoting(s) {
			return strconv.Quote(s)
		}
		return s
	case slog.KindInt64:
		return strconv.FormatInt(v.Int64(), 10)
	case slog.KindUint64:
		return strconv.FormatUint(v.Uint64(), 10)
	case slog.KindFloat64:
		return strconv.FormatFloat(v.Float64(), 'f', -1, 64)
	case slog.KindBool:
		return strconv.FormatBool(v.Bool())
	case slog.KindDuration:
		return v.Duration().String()
	case slog.KindTime:
		return v.Time().Format(time.RFC3339)
	default:
		s := fmt.Sprint(v.Any())
		if needsQuoting(s) {
			return strconv.Quote(s)
		}
		return s
	}
}

func needsQuoting(s string) bool {
	if s == "" {
		return true
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c <= ' ' || c == '"' || c == '=' || c == '{' || c == '}' {
			return true
		}
	}
	return false
}

func (h *ConsoleHandler) writeColored(buf *bytes.Buffer, color, text string) {
	if h.opts.NoColor || color == "" {
		buf.WriteString(text)
		return
	}
	buf.WriteString(color)
	buf.WriteString(text)
	buf.WriteString(ansiReset)
}

func formatConsoleLevel(level slog.Level) (string, string) {
	switch {
	case level >= LevelPanic:
		return "PNC", ansiBold + ansiMagenta
	case level >= LevelFatal:
		return "FTL", ansiBold + ansiRed
	case level >= LevelError:
		return "ERR", ansiRed
	case level >= LevelWarn:
		return "WRN", ansiYellow
	case level >= LevelInfo:
		return "INF", ansiGreen
	case level >= LevelDebug:
		return "DBG", ansiBlue
	default:
		return "TRC", ansiMagenta
	}
}
