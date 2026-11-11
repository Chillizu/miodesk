package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunLogsEmitsNormalizedNDJSONAndHonorsFilters(t *testing.T) {
	isolatedEnv(t)
	bin := t.TempDir()
	journalctl := filepath.Join(bin, "journalctl")
	script := "#!/bin/sh\nprintf '%s\\n' '{\"__REALTIME_TIMESTAMP\":\"1735689600000000\",\"_SYSTEMD_UNIT\":\"user@1000.service\",\"_SYSTEMD_USER_UNIT\":\"mioweave-bridge.service\",\"PRIORITY\":\"4\",\"MESSAGE\":\"{\\\"level\\\":\\\"ERROR\\\",\\\"msg\\\":\\\"Mioweave failed\\\",\\\"request_id\\\":\\\"r-1\\\"}\"}'\n"
	if err := os.WriteFile(journalctl, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	var stdout, stderr bytes.Buffer
	code := Run([]string{"logs", "--source", "mioweave", "--level", "warn", "--grep", "WEAVE", "--json"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit=%d stderr=%s", code, stderr.String())
	}
	if strings.Count(strings.TrimSpace(stdout.String()), "\n") != 0 {
		t.Fatalf("not NDJSON: %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), `"source":"mioweave"`) || !strings.Contains(stdout.String(), `"message":"Mioweave failed"`) || strings.Contains(stdout.String(), `"MESSAGE"`) {
		t.Fatalf("unexpected normalized result: %s", stdout.String())
	}
}

func TestRunLogsRejectsInvalidSource(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := Run([]string{"logs", "--source", "unknown"}, &stdout, &stderr); code != 1 || !strings.Contains(stderr.String(), "invalid journal source") {
		t.Fatalf("exit=%d stderr=%q", code, stderr.String())
	}
}
