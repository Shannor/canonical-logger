# Google Cloud Logging Sinks & Writers

Because `clog` is built on standard `log/slog`, any `io.Writer` or `slog.Handler` works with `clog.NewJSONHandler(w, opts)`.

---

## Option A: Zero-Dependency Stdout JSON (Recommended for Cloud Run, GKE, Kubernetes)

Container platforms automatically ingest structured JSON from `os.Stdout`. For Google Cloud Logging, `clog.GCPReplaceAttr` is built into the zero-dependency core to map the level key to `"severity"` (`"DEBUG"`, `"INFO"`, `"WARNING"`, `"ERROR"`) and message to `"message"`:

```go
package main

import (
	"log/slog"
	"os"

	"github.com/Shannor/canonical-logger"
)

func main() {
	handler := clog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		ReplaceAttr: clog.GCPReplaceAttr,
	})
	slog.SetDefault(slog.New(handler))
}
```

---

## Option B: Custom Google Cloud Logging SDK `io.Writer`

If your application streams logs directly via the `cloud.google.com/go/logging` SDK rather than `os.Stdout`, implement `io.Writer` in your application's logging setup:

```go
package gcpwriter

import (
	"encoding/json"
	"io"

	"cloud.google.com/go/logging"
)

type GCPWriter struct {
	Logger *logging.Logger
}

func (w *GCPWriter) Write(p []byte) (int, error) {
	if w.Logger == nil {
		return len(p), nil
	}

	fields := make(map[string]any)
	if err := json.Unmarshal(p, &fields); err != nil {
		w.Logger.Log(logging.Entry{Payload: string(p)})
		return len(p), nil
	}

	entry := logging.Entry{Payload: fields}
	if sev, ok := fields["severity"].(string); ok {
		entry.Severity = logging.ParseSeverity(sev)
	} else if lvl, ok := fields["level"].(string); ok {
		entry.Severity = logging.ParseSeverity(lvl)
	}

	w.Logger.Log(entry)
	return len(p), nil
}

var _ io.Writer = (*GCPWriter)(nil)
```
