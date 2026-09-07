package logging

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
)

func TestConfigureJSONRedactsCredentialFields(t *testing.T) {
	previous := slog.Default()
	defer slog.SetDefault(previous)

	var buf bytes.Buffer
	if err := Configure(Settings{Level: "debug", Format: "json"}, &buf); err != nil {
		t.Fatal(err)
	}
	slog.Info("test_event", "authorization", "secret-token", "safe", "value")

	var record map[string]any
	if err := json.Unmarshal(buf.Bytes(), &record); err != nil {
		t.Fatalf("invalid JSON log record: %v (%s)", err, buf.String())
	}
	if got := record["authorization"]; got != "[redacted]" {
		t.Errorf("authorization = %v, want redacted", got)
	}
	if got := record["safe"]; got != "value" {
		t.Errorf("safe = %v, want value", got)
	}
	if strings.Contains(buf.String(), "secret-token") {
		t.Error("credential appeared in log output")
	}
}

func TestConfigureRejectsUnknownSettings(t *testing.T) {
	var buf bytes.Buffer
	if err := Configure(Settings{Level: "trace", Format: "text"}, &buf); err == nil {
		t.Error("unknown level should be rejected")
	}
	if err := Configure(Settings{Level: "info", Format: "yaml"}, &buf); err == nil {
		t.Error("unknown format should be rejected")
	}
}

func TestRequestIDContext(t *testing.T) {
	id := NewRequestID()
	if id == "" {
		t.Fatal("NewRequestID returned empty id")
	}
	if got := RequestID(WithRequestID(context.Background(), id)); got != id {
		t.Fatalf("RequestID = %q, want %q", got, id)
	}
}
