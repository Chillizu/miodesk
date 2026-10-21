package codemode

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/Chillizu/miodesk/internal/workspace"
)

type frame struct {
	Kind   string          `json:"kind"`
	ID     int             `json:"id,omitempty"`
	Tool   string          `json:"tool,omitempty"`
	Args   json.RawMessage `json:"args,omitempty"`
	Source string          `json:"source,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  string          `json:"error,omitempty"`
	Output *Output         `json:"output,omitempty"`
}

var slots = make(chan struct{}, 2)

func writeFrame(w io.Writer, f frame) error {
	data, err := json.Marshal(f)
	if err != nil {
		return err
	}
	if len(data) > MaxFrameBytes {
		return errors.New("codemode: IPC frame too large")
	}
	data = append(data, '\n')
	_, err = w.Write(data)
	return err
}
func scanFrame(sc *bufio.Scanner) (frame, error) {
	var f frame
	if !sc.Scan() {
		if err := sc.Err(); err != nil {
			return f, err
		}
		return f, io.EOF
	}
	if err := json.Unmarshal(sc.Bytes(), &f); err != nil {
		return f, err
	}
	return f, nil
}
func scanner(r io.Reader) *bufio.Scanner {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 4096), MaxFrameBytes+1)
	return sc
}

// ServeWorker is the private subprocess entrypoint. Only the three read-only
// tools are brokered by the parent; JS never accesses its filesystem or shell.
// Linux RLIMIT_DATA prevents runaway JS allocations from exhausting Core's
// address space. This is NOT a general-purpose OS filesystem sandbox.
func ServeWorker(in io.Reader, out io.Writer) error {
	if err := enforceWorkerMemoryLimit(); err != nil {
		return err
	}
	sc := scanner(in)
	first, err := scanFrame(sc)
	if err != nil {
		return err
	}
	if first.Kind != "start" {
		return errors.New("codemode: expected start frame")
	}
	nextID := 0
	result, runErr := RunWithDispatch(context.Background(), first.Source, func(ctx context.Context, name string, args json.RawMessage) (any, error) {
		nextID++
		if err := writeFrame(out, frame{Kind: "call", ID: nextID, Tool: name, Args: args}); err != nil {
			return nil, err
		}
		reply, err := scanFrame(sc)
		if err != nil {
			return nil, err
		}
		if reply.Kind != "result" || reply.ID != nextID {
			return nil, errors.New("codemode: invalid response frame")
		}
		if reply.Error != "" {
			return nil, errors.New(reply.Error)
		}
		var value any
		if err := json.Unmarshal(reply.Result, &value); err != nil {
			return nil, err
		}
		return value, nil
	})
	if runErr != nil {
		return writeFrame(out, frame{Kind: "error", Error: boundedError(runErr)})
	}
	return writeFrame(out, frame{Kind: "done", Output: result})
}

func boundedError(err error) string {
	s := err.Error()
	if len(s) > 2048 {
		return s[:2048]
	}
	return s
}

// RunIsolated starts a fresh subprocess under a bounded lifetime and memory
// budget. The parent mediates every tool request and applies path validation.
// Unlike the in-process prototype, script OOM cannot corrupt the Core heap.
// The worker has no namespace/FS sandbox; runtime access is API-whitelisted.
func RunIsolated(parent context.Context, ws *workspace.Workspace, source string) (*Output, error) {
	if runtime.GOOS != "linux" {
		return nil, errors.New("codemode: resource-limited worker currently requires Linux")
	}
	if ws == nil {
		return nil, errors.New("codemode: workspace required")
	}
	if strings.TrimSpace(source) == "" || len(source) > MaxSourceBytes {
		return nil, errors.New("codemode: invalid source size")
	}
	ctx, cancel := context.WithTimeout(parent, 4*time.Second)
	defer cancel()
	select {
	case slots <- struct{}{}:
		defer func() { <-slots }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	binary, err := os.Executable()
	if err != nil {
		return nil, err
	}
	return runIsolatedBinary(ctx, ws, source, binary)
}

func runIsolatedBinary(ctx context.Context, ws *workspace.Workspace, source, binary string) (*Output, error) {
	cmd := exec.CommandContext(ctx, binary, "__codemode_worker")
	// Do not inherit the server's environment, which may contain credentials.
	// The VM needs no ambient PATH, HOME, or provider configuration.
	cmd.Env = []string{"LANG=C"}
	cmd.Dir = "/"
	cmd.Stderr = io.Discard
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("codemode: worker start: %w", err)
	}
	defer func() {
		_ = stdin.Close()
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		_ = cmd.Wait()
	}()
	if err := writeFrame(stdin, frame{Kind: "start", Source: source}); err != nil {
		return nil, fmt.Errorf("codemode: initial IPC: %w", err)
	}
	sc := scanner(stdout)
	calls, totalBytes := 0, 0
	for {
		f, err := scanFrame(sc)
		if err != nil {
			if ctx.Err() != nil {
				return nil, fmt.Errorf("codemode: worker deadline: %w", ctx.Err())
			}
			return nil, fmt.Errorf("codemode: worker ended: %w", err)
		}
		switch f.Kind {
		case "call":
			if f.ID != calls+1 || calls >= MaxCalls {
				return nil, errors.New("codemode: invalid or excessive worker calls")
			}
			calls++
			value, callErr := Dispatch(ctx, ws, f.Tool, f.Args)
			reply := frame{Kind: "result", ID: f.ID}
			if callErr != nil {
				reply.Error = boundedError(callErr)
			} else {
				reply.Result, err = json.Marshal(value)
				if err != nil {
					return nil, err
				}
				totalBytes += len(reply.Result)
				if totalBytes > MaxIntermediateBytes {
					return nil, errors.New("codemode: IPC result budget exceeded")
				}
			}
			if err := writeFrame(stdin, reply); err != nil {
				return nil, fmt.Errorf("codemode: IPC response: %w", err)
			}
		case "done":
			if f.Output == nil || f.Output.Kind != "code" || f.Output.Calls != calls || len(f.Output.Value) > MaxResultBytes {
				return nil, errors.New("codemode: invalid worker result")
			}
			return f.Output, nil
		case "error":
			return nil, fmt.Errorf("codemode: worker: %s", f.Error)
		default:
			return nil, errors.New("codemode: unexpected worker frame")
		}
	}
}
