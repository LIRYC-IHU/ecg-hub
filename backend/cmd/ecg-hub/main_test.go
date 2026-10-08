package main

import (
	"log/slog"
	"testing"
)

func TestRequestLogLevel(t *testing.T) {
	cases := []struct {
		status int
		want   slog.Level
	}{
		{200, slog.LevelDebug},
		{204, slog.LevelDebug},
		{302, slog.LevelDebug},
		{400, slog.LevelWarn},
		{401, slog.LevelWarn},
		{404, slog.LevelWarn},
		// 499 is the last client-error code: it must not reach Error, or a
		// client sending malformed requests would page whoever watches Error.
		{499, slog.LevelWarn},
		{500, slog.LevelError},
		{503, slog.LevelError},
	}
	for _, c := range cases {
		if got := requestLogLevel(c.status); got != c.want {
			t.Errorf("requestLogLevel(%d) = %v, want %v", c.status, got, c.want)
		}
	}
}
