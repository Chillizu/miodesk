// Command mcp-smoke exercises miodesk through its public Streamable HTTP MCP
// endpoint. It prints check names only; it never forwards arbitrary command
// output to stdout.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	shortMarker = "miodesk-smoke-short"
	longMarker  = "miodesk-smoke-long"
	inputMarker = "miodesk-smoke-input"
)

type execOutput struct {
	ExitCode *int   `json:"exit_code"`
	Session  *int   `json:"session_id"`
	Output   string `json:"output"`
}

func main() {
	endpoint := flag.String("endpoint", "", "miodesk Streamable HTTP MCP endpoint")
	flag.Parse()
	if *endpoint == "" || flag.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "usage: miodesk-mcp-smoke -endpoint URL")
		os.Exit(2)
	}
	if err := run(*endpoint); err != nil {
		fmt.Fprintf(os.Stderr, "mcp smoke failed: %v\n", err)
		os.Exit(1)
	}
}

func run(endpoint string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	client := mcp.NewClient(&mcp.Implementation{Name: "miodesk-mcp-smoke", Version: "1.0.0"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: endpoint}, nil)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer session.Close()
	fmt.Println("[OK] connect")

	listed, err := session.ListTools(ctx, &mcp.ListToolsParams{})
	if err != nil {
		return fmt.Errorf("tools/list: %w", err)
	}
	want := map[string]bool{
		"read": true, "search": true, "list": true, "write": true,
		"edit": true, "delete": true, "exec_command": true,
		"write_stdin": true, "context": true, "status": true,
	}
	if len(listed.Tools) != len(want) {
		return fmt.Errorf("tools/list returned %d tools, want %d", len(listed.Tools), len(want))
	}
	for _, tool := range listed.Tools {
		if !want[tool.Name] {
			return fmt.Errorf("unexpected tool name %q", tool.Name)
		}
		delete(want, tool.Name)
	}
	if len(want) != 0 {
		return fmt.Errorf("tools/list missing expected tools")
	}
	fmt.Println("[OK] tools/list")

	statusResult, err := call(ctx, session, "status", map[string]any{})
	if err != nil {
		return fmt.Errorf("status: %w", err)
	}
	statusJSON, err := json.Marshal(statusResult.StructuredContent)
	if err != nil {
		return fmt.Errorf("status payload: %w", err)
	}
	var status struct {
		Kind    string `json:"kind"`
		Name    string `json:"name"`
		Version string `json:"version"`
	}
	if err := json.Unmarshal(statusJSON, &status); err != nil || status.Kind != "status" || status.Name != "miodesk" || status.Version == "" {
		return fmt.Errorf("status returned an unexpected payload")
	}
	fmt.Println("[OK] status")

	short, err := call(ctx, session, "exec_command", map[string]any{
		"cmd": "printf " + shortMarker, "yield_time_ms": 1000, "timeout_ms": 5000,
	})
	if err != nil {
		return fmt.Errorf("short exec: %w", err)
	}
	shortOut, err := decodeOutput(short.StructuredContent)
	if err != nil || shortOut.ExitCode == nil || *shortOut.ExitCode != 0 || !strings.Contains(shortOut.Output, shortMarker) {
		return fmt.Errorf("short exec did not complete with the expected marker")
	}
	fmt.Println("[OK] short exec")

	started, err := call(ctx, session, "exec_command", map[string]any{
		"cmd": "sleep 5; printf " + longMarker, "yield_time_ms": 1, "timeout_ms": 15000,
	})
	if err != nil {
		return fmt.Errorf("long exec start: %w", err)
	}
	out, err := decodeOutput(started.StructuredContent)
	if err != nil || out.Session == nil {
		return fmt.Errorf("long exec did not return a session")
	}
	longOutput := out.Output
	deadline := time.Now().Add(15 * time.Second)
	for out.ExitCode == nil && time.Now().Before(deadline) {
		pollCtx, pollCancel := context.WithDeadline(ctx, deadline)
		result, callErr := call(pollCtx, session, "write_stdin", map[string]any{
			"session_id": *out.Session, "chars": "", "yield_time_ms": 500,
		})
		pollCancel()
		if callErr != nil {
			return fmt.Errorf("long exec poll: %w", callErr)
		}
		out, err = decodeOutput(result.StructuredContent)
		if err != nil {
			return fmt.Errorf("long exec poll result: %w", err)
		}
		if out.ExitCode == nil && out.Session == nil {
			return fmt.Errorf("long exec poll returned neither a live session nor an exit code")
		}
		longOutput += out.Output
	}
	if out.ExitCode == nil || *out.ExitCode != 0 || !strings.Contains(longOutput, longMarker) {
		return fmt.Errorf("long exec did not finish successfully with the expected marker")
	}
	fmt.Println("[OK] long exec poll")

	cat, err := call(ctx, session, "exec_command", map[string]any{
		"cmd": "cat", "yield_time_ms": 1, "timeout_ms": 10000,
	})
	if err != nil {
		return fmt.Errorf("stdin session start: %w", err)
	}
	out, err = decodeOutput(cat.StructuredContent)
	if err != nil || out.Session == nil {
		return fmt.Errorf("cat did not return a session")
	}
	stdinResult, err := call(ctx, session, "write_stdin", map[string]any{
		"session_id": *out.Session, "chars": inputMarker + "\n", "yield_time_ms": 200,
	})
	if err != nil {
		return fmt.Errorf("stdin write: %w", err)
	}
	stdinOut, err := decodeOutput(stdinResult.StructuredContent)
	if err != nil || !strings.Contains(stdinOut.Output, inputMarker) || stdinOut.Session == nil {
		return fmt.Errorf("stdin data was not echoed by the live session")
	}
	cancelResult, err := call(ctx, session, "write_stdin", map[string]any{
		"session_id": *out.Session, "chars": "\u0003", "yield_time_ms": 200,
	})
	if err != nil {
		return fmt.Errorf("stdin session cancel: %w", err)
	}
	cancelOut, err := decodeOutput(cancelResult.StructuredContent)
	if err != nil || cancelOut.Session != nil || cancelOut.ExitCode == nil || *cancelOut.ExitCode == 0 {
		return fmt.Errorf("stdin session did not cancel cleanly")
	}
	fmt.Println("[OK] stdin echo and cancel")
	return nil
}

func call(ctx context.Context, session *mcp.ClientSession, name string, args map[string]any) (*mcp.CallToolResult, error) {
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		return nil, err
	}
	if result.IsError {
		return nil, fmt.Errorf("tool returned an error")
	}
	return result, nil
}

func decodeOutput(value any) (execOutput, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return execOutput{}, err
	}
	var result execOutput
	if err := json.Unmarshal(data, &result); err != nil {
		return execOutput{}, err
	}
	return result, nil
}
