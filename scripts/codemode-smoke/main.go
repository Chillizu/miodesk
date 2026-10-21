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
			if !strings.Contains(tl.Title, "Experimental") || !strings.Contains(tl.Description, "EXPERIMENTAL") {
				panic("experimental status missing from Code Mode tool metadata")
			}
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

	// A script can return any JSON value, not only an object or array. MCP
	// validates StructuredContent against the explicitly declared schema.
	for _, tc := range []struct{ source, want string }{
		{`async () => 42`, `42`},
		{`async () => false`, `false`},
		{`async () => null`, `null`},
		{`async () => ["a", "b"]`, `["a","b"]`},
	} {
		res, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "code", Arguments: map[string]any{"source": tc.source}})
		if err != nil || res == nil || res.IsError {
			// Print the actual diagnostic text, not addresses of MCP content
			// pointers. Fixture scripts here do not use real workspace data.
			var detail []byte
			if res != nil {
				detail, _ = json.Marshal(res.Content)
			}
			panic(fmt.Sprintf("JSON result %q: %v / %s", tc.source, err, detail))
		}
		data, err := json.Marshal(res.StructuredContent)
		if err != nil {
			panic(err)
		}
		var output struct {
			Value json.RawMessage `json:"value"`
		}
		if err := json.Unmarshal(data, &output); err != nil || string(output.Value) != tc.want {
			panic(fmt.Sprintf("wrong JSON value for %s: %s", tc.source, data))
		}
	}
	fmt.Println("[OK] primitive and array JSON results")

	// Exceptions might include sensitive strings constructed from file reads.
	// They must reach the requesting MCP client but never Core's persistent logs.
	failure, err = session.CallTool(ctx, &mcp.CallToolParams{Name: "code", Arguments: map[string]any{
		"source": `async () => {throw new Error("MIODESK_PRIVATE_LOG_SENTINEL");}`,
	}})
	if err != nil || !failure.IsError {
		panic(fmt.Sprintf("expected JavaScript exception, got %v / %+v", err, failure))
	}
	fmt.Println("[OK] JavaScript exception returned to caller")
}
