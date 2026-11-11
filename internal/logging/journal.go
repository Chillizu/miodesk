package logging

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Record is a normalized journal entry.
type Record struct {
	Time    string         `json:"time"`
	Source  string         `json:"source"`
	Level   string         `json:"level"`
	Message string         `json:"message"`
	Fields  map[string]any `json:"fields,omitempty"`
}

// JournalOptions selects records from the fixed set of miodesk services.
type JournalOptions struct {
	Source string
	Lines  int
	Since  string
	Until  string
	Match  string
	Level  string
	Follow bool
}

var journalUnits = map[string]string{
	"core":     "miodesk.service",
	"tunnel":   "miodesk-tunnel.service",
	"mioweave": "mioweave-bridge.service",
}

var credentialKeyReplacer = strings.NewReplacer("_", "", "-", "", ".", "")
var severity = map[string]int{"debug": 0, "info": 1, "warn": 2, "error": 3}

const maxJournalRecord = 1 << 20

// ReadJournal reads bounded JSON journal entries and emits normalized records.
func ReadJournal(ctx context.Context, opts JournalOptions, secrets []string, emit func(Record) error) error {
	if ctx == nil {
		return errors.New("journal context is required")
	}
	if emit == nil {
		return errors.New("journal record callback is required")
	}
	if opts.Source == "" {
		opts.Source = "all"
	}
	if opts.Source != "all" && journalUnits[opts.Source] == "" {
		return errors.New("invalid journal source (choose all, core, tunnel, or mioweave)")
	}
	if opts.Lines < 0 || opts.Lines > 5000 {
		return errors.New("journal lines must be between 0 and 5000")
	}
	if opts.Level == "" {
		opts.Level = "debug"
	}
	opts.Level = strings.ToLower(opts.Level)
	if _, ok := severity[opts.Level]; !ok {
		return errors.New("invalid journal level (choose debug, info, warn, or error)")
	}
	var args []string
	args = append(args, "--user", "--no-pager", "--output=json", "--lines="+strconv.Itoa(opts.Lines))
	if opts.Since != "" {
		since, err := normalizeJournalTime(opts.Since)
		if err != nil {
			return fmt.Errorf("invalid journal since: %w", err)
		}
		args = append(args, "--since="+since)
	}
	if opts.Until != "" {
		until, err := normalizeJournalTime(opts.Until)
		if err != nil {
			return fmt.Errorf("invalid journal until: %w", err)
		}
		args = append(args, "--until="+until)
	}
	if opts.Follow {
		args = append(args, "--follow")
	}
	sources := []string{"core", "tunnel", "mioweave"}
	if opts.Source != "all" {
		sources = []string{opts.Source}
	}
	for _, source := range sources {
		args = append(args, "--unit="+journalUnits[source])
	}
	if runtime.GOOS != "linux" {
		return errors.New("journal logs are available only on Linux with systemd user services")
	}
	cmd := exec.CommandContext(ctx, "journalctl", args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("start journalctl: %w", err)
	}
	var stderr cappedBuffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start journalctl (is systemd installed?): %s", redactText(err.Error(), secrets))
	}
	readErr := scanJournal(stdout, opts, secrets, emit)
	if readErr != nil {
		_ = cmd.Process.Kill()
	}
	waitErr := cmd.Wait()
	if readErr != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		message := readErr.Error()
		safeMessage := redactText(message, secrets)
		if safeMessage == message {
			return readErr
		}
		return errors.New(safeMessage)
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if waitErr != nil {
		detail := strings.TrimSpace(stderr.String())
		if detail != "" {
			return fmt.Errorf("journalctl: %s", redactText(detail, secrets))
		}
		return errors.New("journalctl failed (is the systemd user session available?)")
	}
	return nil
}

func scanJournal(r io.Reader, opts JournalOptions, secrets []string, emit func(Record) error) error {
	s := bufio.NewScanner(r)
	s.Buffer(make([]byte, 64*1024), maxJournalRecord)
	for s.Scan() {
		record, err := parseJournalRecord(s.Bytes())
		if err != nil || record.Source == "" {
			continue
		}
		if severityValue(record.Level) < severity[opts.Level] {
			continue
		}
		record.Message = redactText(record.Message, secrets)
		record.Time = redactText(record.Time, secrets)
		record.Source = redactText(record.Source, secrets)
		record.Level = redactText(record.Level, secrets)
		redactFields(record.Fields, secrets)
		if opts.Match != "" && !strings.Contains(strings.ToLower(record.Message+" "+formatFields(record.Fields)), strings.ToLower(opts.Match)) {
			continue
		}
		if err := emit(record); err != nil {
			return fmt.Errorf("write journal record: %w", err)
		}
	}
	if err := s.Err(); err != nil {
		if strings.Contains(err.Error(), "token too long") {
			return errors.New("journal record exceeds 1 MiB limit")
		}
		return fmt.Errorf("read journal: %w", err)
	}
	return nil
}

func parseJournalRecord(data []byte) (Record, error) {
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return Record{}, err
	}
	r := Record{Fields: make(map[string]any)}
	r.Time = journalTime(raw["__REALTIME_TIMESTAMP"])
	unit, _ := raw["_SYSTEMD_USER_UNIT"].(string)
	if unit == "" {
		unit, _ = raw["_SYSTEMD_UNIT"].(string)
	}
	for source, expected := range journalUnits {
		if unit == expected {
			r.Source = source
			break
		}
	}
	message, _ := raw["MESSAGE"].(string)
	var slogFields map[string]any
	if json.Unmarshal([]byte(message), &slogFields) == nil && slogFields != nil {
		if v, ok := slogFields["msg"]; ok {
			r.Message = fmt.Sprint(v)
		} else if v, ok := slogFields["message"]; ok {
			r.Message = fmt.Sprint(v)
		}
		if v, ok := slogFields["level"]; ok {
			r.Level = normalizeLevel(fmt.Sprint(v))
		}
		if v, ok := slogFields["time"]; ok {
			r.Time = normalizeRecordTime(fmt.Sprint(v))
		}
		for k, v := range slogFields {
			if k != "msg" && k != "message" && k != "level" && k != "time" {
				r.Fields[k] = v
			}
		}
	} else {
		r.Message = message
		parseSlogText(message, &r)
	}
	if r.Level == "" {
		r.Level = normalizePriority(fmt.Sprint(raw["PRIORITY"]))
	}
	if pid, ok := raw["_PID"]; ok {
		r.Fields["pid"] = pid
	}
	if len(r.Fields) == 0 {
		r.Fields = nil
	}
	return r, nil
}

func parseSlogText(message string, r *Record) {
	values := parseTextAttrs(message)
	if level, ok := values["level"]; ok {
		r.Level = normalizeLevel(level)
		if msg, exists := values["msg"]; exists {
			r.Message = msg
		}
		for key, value := range values {
			if key != "time" && key != "level" && key != "msg" {
				r.Fields[key] = value
			}
		}
		if timestamp := values["time"]; timestamp != "" {
			r.Time = normalizeRecordTime(timestamp)
		}
		return
	}
	parts := strings.Fields(message)
	if len(parts) >= 3 {
		level := normalizeLevel(parts[1])
		if level == "debug" || level == "info" || level == "warn" || level == "error" {
			r.Level = level
			r.Message = strings.Join(parts[2:], " ")
		}
	}
}

func parseTextAttrs(text string) map[string]string {
	out := make(map[string]string)
	for i := 0; i < len(text); {
		for i < len(text) && text[i] == ' ' {
			i++
		}
		start := i
		for i < len(text) && text[i] != '=' && text[i] != ' ' {
			i++
		}
		if i == len(text) || text[i] != '=' {
			for i < len(text) && text[i] != ' ' {
				i++
			}
			continue
		}
		key := text[start:i]
		i++
		var value string
		if i < len(text) && text[i] == '"' {
			start = i
			i++
			for i < len(text) {
				if text[i] == '\\' {
					i += 2
					continue
				}
				if text[i] == '"' {
					i++
					break
				}
				i++
			}
			if i > len(text) {
				i = len(text)
			}
			decoded, err := strconv.Unquote(text[start:i])
			if err == nil {
				value = decoded
			}
		} else {
			start = i
			for i < len(text) && text[i] != ' ' {
				i++
			}
			value = text[start:i]
		}
		out[key] = value
	}
	return out
}

func journalTime(value any) string {
	text := fmt.Sprint(value)
	micros, err := strconv.ParseInt(text, 10, 64)
	if err != nil || micros <= 0 {
		return ""
	}
	return time.UnixMicro(micros).UTC().Format(time.RFC3339Nano)
}
func normalizeLevel(v string) string {
	switch strings.ToLower(v) {
	case "10", "debug":
		return "debug"
	case "20", "info":
		return "info"
	case "30", "warn", "warning":
		return "warn"
	case "40", "error", "fatal", "panic":
		return "error"
	}
	return ""
}
func normalizeRecordTime(value string) string {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return value
	}
	return parsed.UTC().Format(time.RFC3339Nano)
}

func normalizePriority(v string) string {
	switch v {
	case "0", "1", "2", "3":
		return "error"
	case "4":
		return "warn"
	case "7":
		return "debug"
	default:
		return "info"
	}
}

func severityValue(level string) int {
	if v, ok := severity[strings.ToLower(level)]; ok {
		return v
	}
	return 1
}

func normalizeJournalTime(value string) (string, error) {
	v := strings.TrimSpace(value)
	if v == "" {
		return "", nil
	}
	if strings.HasSuffix(v, "d") {
		n, err := strconv.Atoi(strings.TrimSuffix(v, "d"))
		if err != nil || n <= 0 {
			return "", fmt.Errorf("expected positive duration or journal date")
		}
		return fmt.Sprintf("%d days ago", n), nil
	}
	if d, err := time.ParseDuration(v); err == nil {
		if d <= 0 {
			return "", errors.New("duration must be positive")
		}
		if d%time.Hour == 0 {
			return fmt.Sprintf("%d hours ago", int(d/time.Hour)), nil
		}
		if d%time.Minute == 0 {
			return fmt.Sprintf("%d minutes ago", int(d/time.Minute)), nil
		}
		return fmt.Sprintf("%d seconds ago", int(d/time.Second)), nil
	}
	return v, nil
}

func redactFields(fields map[string]any, secrets []string) {
	for key, value := range fields {
		safeKey := redactText(key, secrets)
		if safeKey != key {
			delete(fields, key)
		}
		if credentialKey(safeKey) {
			fields[safeKey] = "[redacted]"
			continue
		}
		fields[safeKey] = redactValue(value, secrets)
	}
}

func redactValue(v any, secrets []string) any {
	switch value := v.(type) {
	case string:
		return redactText(value, secrets)
	case map[string]any:
		redactFields(value, secrets)
		return value
	case []any:
		for i := range value {
			value[i] = redactValue(value[i], secrets)
		}
		return value
	default:
		return v
	}
}

func credentialKey(key string) bool {
	k := strings.ToLower(credentialKeyReplacer.Replace(key))
	for _, word := range []string{"token", "password", "passwd", "secret", "authorization", "credential", "apikey", "privatekey", "accesskey", "cookie"} {
		if strings.Contains(k, word) {
			return true
		}
	}
	return false
}

func redactText(value string, secrets []string) string {
	for _, secret := range secrets {
		if secret != "" {
			value = strings.ReplaceAll(value, secret, "[redacted]")
		}
	}
	return value
}
func formatFields(fields map[string]any) string {
	keys := make([]string, 0, len(fields))
	for k := range fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		encoded, err := json.Marshal(fields[k])
		if err != nil {
			encoded = []byte(strconv.Quote(fmt.Sprint(fields[k])))
		}
		fmt.Fprintf(&b, "%s=%s ", k, encoded)
	}
	return b.String()
}

// WriteRecord writes normalized NDJSON or a readable single-line record.
func WriteRecord(w io.Writer, record Record, jsonOutput bool) error {
	if jsonOutput {
		return json.NewEncoder(w).Encode(record)
	}
	message := strings.NewReplacer("\\", "\\\\", "\n", "\\n", "\r", "\\r", "\t", "\\t").Replace(record.Message)
	_, err := fmt.Fprintf(w, "%s [%s] %-5s %s", record.Time, record.Source, strings.ToUpper(record.Level), message)
	if err != nil {
		return err
	}
	if len(record.Fields) > 0 {
		_, err = fmt.Fprintf(w, " %s", formatFields(record.Fields))
	}
	if err == nil {
		_, err = io.WriteString(w, "\n")
	}
	return err
}

type cappedBuffer struct{ bytes.Buffer }

func (b *cappedBuffer) Write(p []byte) (int, error) {
	n := len(p)
	remain := 8192 - b.Len()
	if remain > 0 {
		if len(p) > remain {
			p = p[:remain]
		}
		_, _ = b.Buffer.Write(p)
	}
	return n, nil
}
