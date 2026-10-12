package clog

import (
	"encoding/json"
	"io"

	"cloud.google.com/go/logging"
)

// GCPWriter implements io.Writer and sends logs to Google Cloud Logging via the Cloud Logging SDK.
type GCPWriter struct {
	Logger *logging.Logger
}

func (w *GCPWriter) Write(p []byte) (n int, err error) {
	if w.Logger == nil {
		return len(p), nil
	}

	fields := make(map[string]any)
	// Unmarshal to extract severity and top-level fields so they are promoted to logging.Entry.
	if err := json.Unmarshal(p, &fields); err != nil {
		// If unmarshal fails, send the raw bytes as payload
		w.Logger.Log(logging.Entry{
			Payload: string(p),
		})
		return len(p), nil
	}

	entry := logging.Entry{
		Payload: fields,
	}

	// Map zerolog severity/level string to logging.Severity
	if sev, ok := fields["severity"].(string); ok {
		entry.Severity = logging.ParseSeverity(sev)
	} else if lvl, ok := fields["level"].(string); ok {
		entry.Severity = logging.ParseSeverity(lvl)
	}

	w.Logger.Log(entry)
	return len(p), nil
}

// Ensure GCPWriter implements io.Writer
var _ io.Writer = (*GCPWriter)(nil)
