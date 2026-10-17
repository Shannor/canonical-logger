// Package cloggcp provides Google Cloud Logging integration for canonical-logger (clog).
package cloggcp

import (
	"encoding/json"
	"io"

	"cloud.google.com/go/logging"
	"github.com/Shannor/canonical-logger"
)

// ReplaceAttr maps slog.Level keys to Google Cloud Logging "severity" strings
// ("DEBUG", "INFO", "WARNING", "ERROR") and the message key to "message".
var ReplaceAttr = clog.GCPReplaceAttr

// Writer implements io.Writer and sends logs to Google Cloud Logging via the Cloud Logging SDK.
type Writer struct {
	Logger *logging.Logger
}

// GCPWriter is an alias for Writer for drop-in compatibility.
type GCPWriter = Writer

func (w *Writer) Write(p []byte) (n int, err error) {
	if w.Logger == nil {
		return len(p), nil
	}

	fields := make(map[string]any)
	if err := json.Unmarshal(p, &fields); err != nil {
		w.Logger.Log(logging.Entry{
			Payload: string(p),
		})
		return len(p), nil
	}

	entry := logging.Entry{
		Payload: fields,
	}

	if sev, ok := fields["severity"].(string); ok {
		entry.Severity = logging.ParseSeverity(sev)
	} else if lvl, ok := fields["level"].(string); ok {
		entry.Severity = logging.ParseSeverity(lvl)
	}

	w.Logger.Log(entry)
	return len(p), nil
}

var _ io.Writer = (*Writer)(nil)
