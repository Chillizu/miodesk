package logging

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseJournalRecordNestedSlogAndRedaction(t *testing.T) {
	input := `{"__REALTIME_TIMESTAMP":"1735689600123456","_SYSTEMD_UNIT":"user@1000.service","_SYSTEMD_USER_UNIT":"mioweave-bridge.service","_PID":"123","__CURSOR":"cursor-value","SYSLOG_IDENTIFIER":"mioweave-bridge","PRIORITY":"6","MESSAGE":"{\"time\":\"2025-01-01T00:00:00Z\",\"level\":\"INFO\",\"msg\":\"token=private-token\\ncontinued\",\"request_id\":\"req-1\",\"duration_ms\":12,\"authorization\":\"Bearer raw-secret\"}"}`
	r, err := parseJournalRecord([]byte(input))
	if err != nil {
		t.Fatal(err)
	}
	if r.Source != "mioweave" || r.Level != "info" || r.Time != "2025-01-01T00:00:00Z" {
		t.Fatalf("record metadata: %+v", r)
	}
	if err := scanJournal(strings.NewReader(input+"\n"), JournalOptions{Level: "debug"}, []string{"private-token", "raw-secret"}, func(got Record) error { r = got; return nil }); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(r.Message, "private-token") || r.Fields["authorization"] != "[redacted]" {
		t.Fatalf("secrets not redacted: %+v", r)
	}
	if r.Fields["request_id"] != "req-1" || r.Fields["duration_ms"] != float64(12) || r.Fields["pid"] != "123" {
		t.Fatalf("useful fields lost: %+v", r.Fields)
	}
	if _, exists := r.Fields["__CURSOR"]; exists {
		t.Fatalf("journald metadata leaked into fields: %+v", r.Fields)
	}
}
func TestParseJournalRecordRejectsUnknownUnitAndNormalizesTime(t *testing.T) {
	input := `{"__REALTIME_TIMESTAMP":"1735689600000000","_SYSTEMD_UNIT":"unrelated.service","PRIORITY":"7","MESSAGE":"{\"time\":\"2025-01-01T01:00:00+01:00\",\"level\":\"mystery\",\"msg\":\"message\"}"}`
	record, err := parseJournalRecord([]byte(input))
	if err != nil {
		t.Fatal(err)
	}
	if record.Source != "" || record.Level != "debug" || record.Time != "2025-01-01T00:00:00Z" {
		t.Fatalf("unexpected normalized record: %+v", record)
	}
}

func TestScanJournalFiltersAndEscapesReadableOutput(t *testing.T) {
	message, _ := json.Marshal(map[string]any{"level": "ERROR", "msg": "A\nB", "api_key": "private"})
	entry, _ := json.Marshal(map[string]any{"_SYSTEMD_UNIT": "user@1000.service", "_SYSTEMD_USER_UNIT": "miodesk.service", "PRIORITY": "4", "MESSAGE": string(message)})
	input := "{\"_SYSTEMD_USER_UNIT\":\"miodesk.service\",\"PRIORITY\":\"7\",\"MESSAGE\":\"debug record\"}\n" + string(entry) + "\n"
	var records []Record
	err := scanJournal(strings.NewReader(input), JournalOptions{Level: "warn", Match: "b"}, []string{"private"}, func(r Record) error { records = append(records, r); return nil })
	if err != nil || len(records) != 1 {
		t.Fatalf("records=%+v err=%v", records, err)
	}
	records[0].Fields["note"] = "x\ny"
	var out bytes.Buffer
	if err := WriteRecord(&out, records[0], false); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "\nB") || strings.Contains(out.String(), "private") || !strings.Contains(out.String(), `A\nB`) || !strings.Contains(out.String(), `note="x\ny"`) {
		t.Fatalf("unsafe/unreadable output: %q", out.String())
	}
}
func TestRedactFieldsMutatesNestedValuesAndKeys(t *testing.T) {
	nested := map[string]any{"access_token": "secret-value"}
	list := []any{"secret-value"}
	fields := map[string]any{"field-secret-value": "safe", "nested": nested, "list": list}
	redactFields(fields, []string{"secret-value"})
	encoded, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "secret-value") {
		t.Fatalf("secret remained after in-place redaction: %s", encoded)
	}
	if _, ok := fields["field-[redacted]"]; !ok {
		t.Fatalf("secret in field key was not redacted: %+v", fields)
	}
	if nested["access_token"] != "[redacted]" || list[0] != "[redacted]" {
		t.Fatalf("nested values were not redacted in place: %+v", fields)
	}
}

func TestReadJournalValidatesBeforeStartingJournalctl(t *testing.T) {
	for _, opts := range []JournalOptions{{Source: "bogus"}, {Lines: -1}, {Lines: 5001}, {Level: "verbose"}, {Since: "-1s"}} {
		if err := ReadJournal(context.Background(), opts, nil, func(Record) error { return nil }); err == nil {
			t.Errorf("accepted invalid options: %+v", opts)
		}
	}
}

func TestReadJournalRequiresCallback(t *testing.T) {
	if err := ReadJournal(context.Background(), JournalOptions{}, nil, nil); err == nil {
		t.Fatal("nil callback accepted")
	}
}
func TestReadJournalPreservesCallbackError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "journalctl")
	line := `{"_SYSTEMD_UNIT":"user@1000.service","_SYSTEMD_USER_UNIT":"mioweave-bridge.service","PRIORITY":"6","MESSAGE":"hello"}`
	script := "#!/bin/sh\nprintf '%s\\n' '" + line + "'\n"
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	sentinel := errors.New("panel response limit")
	err := ReadJournal(context.Background(), JournalOptions{Source: "mioweave", Level: "debug"}, nil, func(Record) error { return sentinel })
	if !errors.Is(err, sentinel) {
		t.Fatalf("callback error identity lost: %v", err)
	}
}

func TestReadJournalCancellationStopsJournalctl(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "journalctl")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexec sleep 10\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(100*time.Millisecond, cancel)
	err := ReadJournal(ctx, JournalOptions{Source: "mioweave", Lines: 0, Level: "debug", Follow: true}, nil, func(Record) error { return nil })
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation error = %v", err)
	}
}

func TestScanJournalRejectsOversizedRecord(t *testing.T) {
	input := strings.Repeat("x", maxJournalRecord+1)
	err := scanJournal(strings.NewReader(input), JournalOptions{Level: "debug"}, nil, func(Record) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "1 MiB") {
		t.Fatalf("oversized record error = %v", err)
	}
}
