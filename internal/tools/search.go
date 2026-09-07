package tools

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"miodesk/internal/workspace"
)

const (
	DefaultSearchLimit = 100
	MaxSearchLimit     = 1000
	// matchTextLimit keeps one match line from flooding the result.
	matchTextLimit = 200
	// fallbackMaxFileBytes skips huge files in the built-in engine.
	fallbackMaxFileBytes = int64(1 << 20)
	// rgTimeout bounds a ripgrep run; a hung external binary must not hang
	// the tool.
	rgTimeout = 30 * time.Second
	// maxSearchOutput bounds the output buffered from rg. --max-count is per
	// file, not global, so a workspace with many matching files could otherwise
	// make one request consume unbounded memory before result parsing.
	maxSearchOutput = 8 << 20
)

type SearchInput struct {
	Query         string `json:"query" jsonschema:"text to search for"`
	Path          string `json:"path,omitempty" jsonschema:"directory or file to search within (default: workspace root)"`
	MaxResults    int    `json:"max_results,omitempty" jsonschema:"maximum number of matches (default 100)"`
	CaseSensitive bool   `json:"case_sensitive,omitempty" jsonschema:"match case exactly (default false)"`
}

type Match struct {
	File string `json:"file"` // workspace-relative
	Line int    `json:"line"`
	Text string `json:"text"`
}

type SearchOutput struct {
	Kind      string  `json:"kind"` // "search"
	Query     string  `json:"query"`
	Matches   []Match `json:"matches"`
	Truncated bool    `json:"truncated"`
	Engine    string  `json:"engine"` // ripgrep | builtin
}

var (
	rgOnce        sync.Once
	rgPathValue   string
	rgDisabledFor bool // test hook: force the built-in engine
)

// rgPath reports the ripgrep binary path, or "" when unavailable. ripgrep is
// an optimization, never a dependency.
func rgPath() string {
	if rgDisabledFor {
		return ""
	}
	rgOnce.Do(func() {
		if p, err := exec.LookPath("rg"); err == nil {
			rgPathValue = p
		}
	})
	return rgPathValue
}

// Search finds text matches inside the workspace. It uses ripgrep when the
// binary is present and falls back to a built-in walker otherwise.
func Search(ctx context.Context, ws *workspace.Workspace, in SearchInput) (*SearchOutput, error) {
	if strings.TrimSpace(in.Query) == "" {
		return nil, fmt.Errorf("search: query must not be empty")
	}
	path, err := ws.Resolve(in.Path)
	if err != nil {
		return nil, err
	}
	limit := in.MaxResults
	if limit <= 0 {
		limit = DefaultSearchLimit
	}
	if limit > MaxSearchLimit {
		limit = MaxSearchLimit
	}

	if rg := rgPath(); rg != "" {
		return searchRipgrep(ctx, rg, ws, path, in, limit)
	}
	return searchBuiltin(ctx, ws, path, in, limit)
}

func searchRipgrep(ctx context.Context, rg string, ws *workspace.Workspace, path string, in SearchInput, limit int) (*SearchOutput, error) {
	ctx, cancel := context.WithTimeout(ctx, rgTimeout)
	defer cancel()

	args := []string{"-n", "--no-heading", "--max-columns", fmt.Sprint(matchTextLimit), "--max-count", fmt.Sprint(limit)}
	if !in.CaseSensitive {
		args = append(args, "-i")
	}
	args = append(args, "-e", in.Query, path)

	var stdout searchOutputBuffer
	stdout.max = maxSearchOutput
	var stderr limitedBuffer
	stderr.max = 64 << 10
	cmd := exec.CommandContext(ctx, rg, args...)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
			// Exit code 1 means "no matches", not failure.
			return &SearchOutput{Kind: "search", Query: in.Query, Matches: []Match{}, Engine: "ripgrep"}, nil
		}
		if ctx.Err() != nil {
			return nil, fmt.Errorf("search: ripgrep timed out: %w", ctx.Err())
		}
		return nil, fmt.Errorf("search: ripgrep failed: %s", strings.TrimSpace(stderr.String()))
	}

	out := &SearchOutput{Kind: "search", Query: in.Query, Matches: []Match{}, Engine: "ripgrep"}
	out.Truncated = stdout.Truncated()
	sc := bufio.NewScanner(&stdout.data)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		file, line, text, ok := splitRipgrepLine(sc.Text())
		if !ok {
			continue
		}
		out.Matches = append(out.Matches, Match{File: ws.Rel(file), Line: line, Text: text})
		if len(out.Matches) >= limit {
			out.Truncated = true
			break
		}
	}
	return out, sc.Err()
}

// searchOutputBuffer keeps the beginning of rg output so parsed matches stay
// deterministic when the safety cap is reached. Write reports success to let
// the child finish and avoids an unbounded pipe/memory growth path.
type searchOutputBuffer struct {
	data      bytes.Buffer
	max       int
	truncated bool
}

func (b *searchOutputBuffer) Write(p []byte) (int, error) {
	if room := b.max - b.data.Len(); room > 0 {
		if len(p) > room {
			_, _ = b.data.Write(p[:room])
			b.truncated = true
		} else {
			_, _ = b.data.Write(p)
		}
	} else {
		b.truncated = true
	}
	return len(p), nil
}

func (b *searchOutputBuffer) Truncated() bool { return b.truncated }

// splitRipgrepLine parses "file:line:text" from `rg --no-heading -n` output.
func splitRipgrepLine(s string) (file string, line int, text string, ok bool) {
	i := strings.Index(s, ":")
	if i < 0 {
		return "", 0, "", false
	}
	file, rest := s[:i], s[i+1:]
	j := strings.Index(rest, ":")
	if j < 0 {
		return "", 0, "", false
	}
	n := 0
	for _, r := range rest[:j] {
		if r < '0' || r > '9' {
			return "", 0, "", false
		}
		n = n*10 + int(r-'0')
	}
	return file, n, rest[j+1:], true
}

func searchBuiltin(ctx context.Context, ws *workspace.Workspace, path string, in SearchInput, limit int) (*SearchOutput, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	out := &SearchOutput{Kind: "search", Query: in.Query, Matches: []Match{}, Engine: "builtin"}
	if !fi.IsDir() {
		out.Matches = searchOneFile(ws, path, in, limit, 0)
		out.Truncated = len(out.Matches) >= limit
		return out, nil
	}

	root := path
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, werr error) error {
		if werr != nil {
			return werr
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if d.IsDir() {
			if p != root && strings.HasPrefix(d.Name(), ".") {
				return fs.SkipDir
			}
			return nil
		}
		if d.Type()&fs.ModeSymlink != 0 {
			return nil // do not follow symlinks; sandbox re-checks targets
		}
		out.Matches = append(out.Matches, searchOneFile(ws, p, in, limit-len(out.Matches), fallbackMaxFileBytes)...)
		if len(out.Matches) >= limit {
			out.Truncated = true
			return fs.SkipAll
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// searchOneFile returns matches from one file, capped at remaining.
// sizeCap of 0 means no file-size limit.
func searchOneFile(ws *workspace.Workspace, path string, in SearchInput, remaining int, sizeCap int64) []Match {
	if remaining <= 0 {
		return nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	if sizeCap > 0 {
		if fi, err := f.Stat(); err != nil || fi.Size() > sizeCap {
			return nil
		}
	}

	query := []byte(in.Query)
	if !in.CaseSensitive {
		query = []byte(strings.ToLower(in.Query))
	}

	var matches []Match
	sc := bufio.NewScanner(io.LimitReader(f, fallbackMaxFileBytes))
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	lineNo := 0
	for sc.Scan() {
		lineNo++
		hay := sc.Bytes()
		if !in.CaseSensitive {
			hay = []byte(strings.ToLower(string(hay)))
		}
		if !bytes.Contains(hay, query) {
			continue
		}
		text := sc.Text()
		if len(text) > matchTextLimit {
			text = text[:matchTextLimit]
		}
		matches = append(matches, Match{File: ws.Rel(path), Line: lineNo, Text: text})
		if len(matches) >= remaining {
			return matches
		}
	}
	return matches
}
