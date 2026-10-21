package codemode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Chillizu/miodesk/internal/tools"
	"github.com/Chillizu/miodesk/internal/workspace"
	"github.com/dop251/goja"
)

const (
	MaxSourceBytes       = 16 << 10
	MaxCalls             = 16
	MaxResultBytes       = 32 << 10
	MaxIntermediateBytes = 512 << 10
	MaxDuration          = 2 * time.Second
	MaxFrameBytes        = 256 << 10
)

type Input struct {
	Source string `json:"source" jsonschema:"JavaScript async arrow function returning a small JSON value, e.g. async () => { const x=await tools.list({path:'project'}); return x.entries.map(e=>e.path); }"`
}

type Output struct {
	Kind  string          `json:"kind"`
	Value json.RawMessage `json:"value"`
	Calls int             `json:"calls"`
}

// Dispatch is the ONLY allowlisted host interface visible to a Code Mode
// script. This method runs on the trusted host, not the child VM.
func Dispatch(ctx context.Context, ws *workspace.Workspace, name string, args json.RawMessage) (any, error) {
	if ws == nil {
		return nil, errors.New("codemode: workspace required")
	}
	if len(args) > 16<<10 {
		return nil, errors.New("codemode: arguments too large")
	}
	switch name {
	case "read":
		var in tools.ReadInput
		if err := decodeArgs(args, &in); err != nil {
			return nil, err
		}
		if in.Limit <= 0 || in.Limit > 100 {
			in.Limit = 100
		}
		out, err := tools.Read(ctx, ws, in)
		if err != nil {
			return nil, err
		}
		if len(out.Content) > 128<<10 {
			out.Content = out.Content[:128<<10]
			out.Truncated = true
		}
		return out, nil
	case "search":
		var in tools.SearchInput
		if err := decodeArgs(args, &in); err != nil {
			return nil, err
		}
		if in.MaxResults <= 0 || in.MaxResults > 50 {
			in.MaxResults = 50
		}
		return tools.Search(ctx, ws, in)
	case "list":
		var in tools.ListInput
		if err := decodeArgs(args, &in); err != nil {
			return nil, err
		}
		if in.Limit <= 0 || in.Limit > 100 {
			in.Limit = 100
		}
		if in.Depth <= 0 || in.Depth > 3 {
			in.Depth = 3
		}
		return tools.List(ctx, ws, in)
	default:
		return nil, fmt.Errorf("codemode: tool %q not allowed", name)
	}
}

func decodeArgs(raw json.RawMessage, out any) error {
	if len(raw) == 0 || raw[0] != '{' {
		return errors.New("codemode: tool arguments must be an object")
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("codemode: invalid tool arguments: %w", err)
	}
	return nil
}

// Run is an internal prototype convenience wrapper. The model-facing surface
// uses RunIsolated, which moves all JS execution to a child process.
func Run(ctx context.Context, ws *workspace.Workspace, source string) (*Output, error) {
	if ws == nil {
		return nil, errors.New("codemode: workspace required")
	}
	return RunWithDispatch(ctx, source, func(ctx context.Context, name string, args json.RawMessage) (any, error) {
		return Dispatch(ctx, ws, name, args)
	})
}

// RunWithDispatch must run exclusively on a dedicated JS worker when invoked
// from remotely supplied MCP scripts. Goja by itself is not an OS sandbox.
func RunWithDispatch(parent context.Context, source string, dispatch func(context.Context, string, json.RawMessage) (any, error)) (*Output, error) {
	if strings.TrimSpace(source) == "" || len(source) > MaxSourceBytes {
		return nil, fmt.Errorf("codemode: source must be nonempty and at most %d bytes", MaxSourceBytes)
	}
	if dispatch == nil {
		return nil, errors.New("codemode: missing host dispatcher")
	}
	ctx, cancel := context.WithTimeout(parent, MaxDuration)
	defer cancel()
	vm := goja.New()
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			vm.Interrupt(ctx.Err())
		case <-done:
		}
	}()
	calls, intermediate := 0, 0
	namespace := vm.NewObject()
	for _, name := range []string{"read", "search", "list"} {
		toolName := name
		if err := namespace.Set(name, func(call goja.FunctionCall) goja.Value {
			if err := ctx.Err(); err != nil {
				panic(vm.NewGoError(err))
			}
			if calls >= MaxCalls {
				panic(vm.NewGoError(fmt.Errorf("codemode: maximum %d tool calls", MaxCalls)))
			}
			args, err := json.Marshal(call.Argument(0).Export())
			if err != nil {
				panic(vm.NewGoError(err))
			}
			calls++
			value, err := dispatch(ctx, toolName, args)
			if err != nil {
				panic(vm.NewGoError(err))
			}
			raw, err := json.Marshal(value)
			if err != nil {
				panic(vm.NewGoError(err))
			}
			intermediate += len(raw)
			if intermediate > MaxIntermediateBytes {
				panic(vm.NewGoError(fmt.Errorf("codemode: intermediate results exceed %d bytes", MaxIntermediateBytes)))
			}
			var plain any
			if err := json.Unmarshal(raw, &plain); err != nil {
				panic(vm.NewGoError(err))
			}
			return vm.ToValue(plain)
		}); err != nil {
			return nil, err
		}
	}
	if err := vm.Set("tools", namespace); err != nil {
		return nil, err
	}
	value, err := vm.RunString("(" + source + ")()")
	if err != nil {
		return nil, fmt.Errorf("codemode: execute: %w", err)
	}
	if promise, ok := value.Export().(*goja.Promise); ok {
		switch promise.State() {
		case goja.PromiseStateFulfilled:
			value = promise.Result()
		case goja.PromiseStateRejected:
			return nil, fmt.Errorf("codemode: script rejected: %v", promise.Result())
		default:
			return nil, errors.New("codemode: unresolved async operation")
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("codemode: %w", err)
	}
	raw, err := json.Marshal(value.Export())
	if err != nil {
		return nil, fmt.Errorf("codemode: result must be JSON serializable: %w", err)
	}
	if len(raw) > MaxResultBytes {
		return nil, fmt.Errorf("codemode: result exceeds %d bytes; filter or slice inside script", MaxResultBytes)
	}
	return &Output{Kind: "code", Value: raw, Calls: calls}, nil
}
