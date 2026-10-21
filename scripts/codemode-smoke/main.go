// Command codemode-smoke verifies optional Code Mode over the public MCP transport
// against a disposable, explicitly enabled miodesk Core server.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"os"
	"strings"
	"time"
)

func main() {
	endpoint := flag.String("endpoint", "", "isolated enabled MCP endpoint")
	flag.Parse()
	if *endpoint == "" {
		fmt.Fprintln(os.Stderr, "-endpoint required")
		os.Exit(2)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client := mcp.NewClient(&mcp.Implementation{Name: "miodesk-codemode-smoke", Version: "test"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: *endpoint}, nil)
	if err != nil {
		panic(err)
	}
	defer session.Close()
	listed, err := session.ListTools(ctx, &mcp.ListToolsParams{})
	if err != nil {
		panic(err)
	}
	if len(listed.Tools) != 11 {
		panic(fmt.Sprintf("expected 11 tools, got %d", len(listed.Tools)))
	}
	found := false
	for _, tl := range listed.Tools {
		if tl.Name == "code" {
			found = true
		}
	}
	if !found {
		panic("code missing")
	}
	fmt.Println("[OK] enabled tools/list includes code")
	call, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "code", Arguments: map[string]any{
		"source": `async () => { const items = (await tools.list({path:"project"})).entries;
  const f=items.find(x=>x.name==="hello.txt");
  const text=await tools.read({path:f.path,offset:1,limit:1});
  return {content:text.content.trim(),paths:items.map(x=>x.path)}; }`,
	}})
	if err != nil {
		panic(err)
	}
	if call.IsError {
		panic(fmt.Sprintf("code rejected: %v", call.Content))
	}
	raw, err := json.Marshal(call.StructuredContent)
	if err != nil {
		panic(err)
	}
	var out struct {
		Kind  string `json:"kind"`
		Calls int    `json:"calls"`
		Value struct {
			Content string   `json:"content"`
			Paths   []string `json:"paths"`
		} `json:"value"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		panic(err)
	}
	if out.Kind != "code" || out.Calls != 2 || out.Value.Content != "hello codemode" || len(out.Value.Paths) != 1 || !strings.HasSuffix(out.Value.Paths[0], "project/hello.txt") {
		panic(fmt.Sprintf("unexpected %s", raw))
	}
	fmt.Println("[OK] Core-brokered JS list/read composition")
	failure, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "code", Arguments: map[string]any{"source": `async () => await tools.write({path:"project/hacked.txt",content:"bad"})`}})
	if err != nil {
		panic(err)
	}
	if !failure.IsError {
		panic("write action was not rejected")
	}
	fmt.Println("[OK] writes are unavailable")
}
