package main

import (
	"log/slog"
	"os"
	"strings"
)

// newLogger gera logs JSON no mesmo formato dos demais serviços do ToggleMaster
// (timestamp, level, service, event), para que o Alloy/Loki os indexem sem ajustes.
func newLogger() *slog.Logger {
	handler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
		ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
			switch a.Key {
			case slog.TimeKey:
				a.Key = "timestamp"
			case slog.MessageKey:
				a.Key = "event"
			case slog.LevelKey:
				a.Value = slog.StringValue(strings.ToLower(a.Value.String()))
			}
			return a
		},
	})

	return slog.New(handler).With("service", "self-healing")
}
