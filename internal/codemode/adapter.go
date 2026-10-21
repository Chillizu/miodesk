// Package codemode prototypes a bounded JavaScript composition surface over
// existing read-only miodesk tools. It is intentionally not registered as an
// MCP tool until an OS-isolated worker and memory budget are implemented.
package codemode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/dop251/goja"

	"github.com/Chillizu/miodesk/internal/tools"
	"github.com/Chillizu/miodesk/internal/workspace"
)

const (
	MaxSourceBytes       = 16 << 10
	MaxCalls             = 16
	MaxResultBytes       = 32 << 10
	MaxIntermediateBytes = 512 << 10
	MaxDuration          = 2 * time.Second
)

type Output struct {
	Kind  string          `json:"kind"`
	Value json.RawMessage `json:"value"`
	Calls int             `json:"calls"`
}

// Run executes one async-arrow JavaScript function with only tools.read,
// tools.search, and tools.list available. Each callback goes through the same
// workspace validation and limits as the native file tools.
//
// This VM runs in-process; it is NOT an OS sandbox for untrusted code. Do not
// advertise this prototype over public MCP until its execution is isolated
// in a resource-limited subprocess.
func Run(parent context.Context, ws *workspace.Workspace, source string) (*Output, error) {
	if ws == nil {
		return nil, errors.New("codemode: workspace required")
	}
	if strings.TrimSpace(source) == "" || len(source) > MaxSourceBytes {
		return nil, fmt.Errorf("codemode: source must be nonempty and at most %d bytes", MaxSourceBytes)
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
	attach := func(name string, invoke func(goja.Value) (any, error)) error {
		return namespace.Set(name, func(call goja.FunctionCall) goja.Value {
			if err := ctx.Err(); err != nil {
				panic(vm.NewGoError(err))
			}
			if calls >= MaxCalls {
				panic(vm.NewGoError(fmt.Errorf("codemode: maximum %d tool calls", MaxCalls)))
			}
			calls++
			result, err := invoke(call.Argument(0))
			if err != nil {
				panic(vm.NewGoError(err))
			}
			raw, err := json.Marshal(result)
			if err != nil {
				panic(vm.NewGoError(err))
			}
			intermediate += len(raw)
			if intermediate > MaxIntermediateBytes {
				panic(vm.NewGoError(fmt.Errorf("codemode: intermediate results exceed %d bytes", MaxIntermediateBytes)))
			}
			// Only JSON-decoded Go values cross into JS; no Go pointers,
			// ambient OS APIs, workspace handles or native tool internals.
			var plain any
			if err := json.Unmarshal(raw, &plain); err != nil {
				panic(vm.NewGoError(err))
			}
			return vm.ToValue(plain)
		})
	}

	decode := func(arg goja.Value, dest any) error {
		if goja.IsUndefined(arg) || goja.IsNull(arg) {
			return errors.New("codemode: tool argument must be an object")
		}
		raw, err := json.Marshal(arg.Export())
		if err != nil {
			return fmt.Errorf("codemode: encode tool argument: %w", err)
		}
		if len(raw) > 16<<10 {
			return errors.New("codemode: tool argument is too large")
		}
		if err := json.Unmarshal(raw, dest); err != nil {
			return fmt.Errorf("codemode: invalid tool argument: %w", err)
		}
		return nil
	}
	if err := attach("read", func(arg goja.Value) (any, error) {
		var in tools.ReadInput
		if err := decode(arg, &in); err != nil {
			return nil, err
		}
		if in.Limit <= 0 || in.Limit > 100 {
			in.Limit = 100
		}
		result, err := tools.Read(ctx, ws, in)
		if err != nil {
			return nil, err
		}
		// No single nested result should exhaust the cumulative budget.
		if len(result.Content) > 128<<10 {
			result.Content = result.Content[:128<<10]
			result.Truncated = true
		}
		return result, nil
	}); err != nil {
		return nil, err
	}
	if err := attach("search", func(arg goja.Value) (any, error) {
		var in tools.SearchInput
		if err := decode(arg, &in); err != nil {
			return nil, err
		}
		if in.MaxResults <= 0 || in.MaxResults > 50 {
			in.MaxResults = 50
		}
		return tools.Search(ctx, ws, in)
	}); err != nil {
		return nil, err
	}
	if err := attach("list", func(arg goja.Value) (any, error) {
		var in tools.ListInput
		if err := decode(arg, &in); err != nil {
			return nil, err
		}
		if in.Limit <= 0 || in.Limit > 100 {
			in.Limit = 100
		}
		if in.Depth <= 0 || in.Depth > 3 {
			in.Depth = 3
		}
		return tools.List(ctx, ws, in)
	}); err != nil {
		return nil, err
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
			return nil, errors.New("codemode: unresolved async operation (only native tools are available)")
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("codemode: %w", err)
	}
	result, err := json.Marshal(value.Export())
	if err != nil {
		return nil, fmt.Errorf("codemode: result must be JSON serializable: %w", err)
	}
	if len(result) > MaxResultBytes {
		return nil, fmt.Errorf("codemode: result exceeds %d bytes; filter or slice inside script", MaxResultBytes)
	}
	return &Output{Kind: "code", Value: json.RawMessage(result), Calls: calls}, nil
}
