# Code Mode prototype — 2026-10-21

Status: **research/prototype only; not installed, registered, or enabled**.

## Goal and boundaries

Create a Codex-inspired JavaScript composition surface **inside the miodesk
Core repository**, not inside or coupled to OpenAI Secure MCP Tunnel.
Eventually Core may optionally advertise a `code` MCP tool for clients that
benefit from reduced round-trips. Default native tools must remain usable.

Reference design:
- Codex uses a JS Code Mode execution host with internal tool dispatch and an
  `exec` / `wait` interface; this prototype is **not** that V8 runtime.
- Cloudflare describes a single-`code` MCP tool for wrapping a small set of
  upstream tools. For miodesk's ten-tool catalog, a full discovery/search
  catalog would add complexity without benefit.

## What works now

`internal/codemode.Run(ctx, workspace, source)` creates a fresh Goja JS
runtime. The script is an async arrow function such as:

```js
async () => {
  const hits = await tools.search({ query: "TODO", path: "project", max_results: 10 });
  const details = [];
  for (const hit of hits.matches) {
    const text = await tools.read({ path: hit.file, offset: hit.line, limit: 1 });
    details.push({ file: hit.file, line: hit.line, content: text.content });
  }
  return details;
}
```

The only bound host methods are `tools.read`, `tools.search`, and
`tools.list`. All call the **same native implementation** and workspace
resolution as ordinary miodesk tools. There are no Node.js builtins, direct
network methods, shell methods, write APIs, or credentials exposed to JS.

Prototype limits: <= 16 KiB source, <= 16 nested calls, <= 512 KiB total
intermediate serialized output, <= 32 KiB final result, 2 seconds of execution.
Nested read is further limited to 100 lines and 128 KiB per result;
search/list are clamped. Tests cover composed operations, literal matching,
path escapes, absence of write and shell APIs, output caps, call caps,
and cancellation of an infinite loop.

## Why the endpoint is *not* exposed yet

Goja is an **in-process language VM**, not a security or memory sandbox.
A script can create large JavaScript objects before the Go cancellation
interrupt is processed. There is no reliable per-runtime heap cap.
A JS bug or memory exhaustion would endanger the Core and disrupt the
independent OpenAI Tunnel's access to it.

Consequently **do not** call `mcp.AddTool` on this runtime, and do not enable
it through `config.toml` yet. The default Core continues to advertise the
original ten tools.

## Acceptance criteria for the next step

1. Move JavaScript execution into a subprocess with an OS-enforced memory
   budget and kill-on-timeout. Keep ambient filesystem/network capabilities
   unavailable to executed JS. Do not claim that a Goja process by itself is
   a complete kernel sandbox.
2. Broker *only* allowlisted native read-only tool calls via bounded IPC;
   enforce the same workspace path policy and tool-level limits in the
   privileged Core, rather than trusting the child worker.
3. Ensure cancellation tears down subprocesses even after a blocked/failed
   tool call. Enforce request byte limits, intermediate result budgets,
   concurrency limits and structured error reporting. Run cross-platform tests.
4. Introduce a Core-level opt-in (`[codemode] enabled = true`) only after the
   isolation and DoS tests pass. Then add the optional tool to Core
   `tools/list`; no changes to the Tunnel component, its credentials, or the
   existing ten tool signatures.
5. Validate against real MCP clients and benchmark end-to-end latency,
   intermediate tokens, success rate and failures on the same task suite.
   Do not claim token/latency savings from the existence of the adapter alone.

## Local overhead baseline

Two 100-iteration runs (Intel Core Ultra 9 185H, 2026-10-21) of the same
`list → read` composition reported direct-tool local work at 13.5–25.5
microseconds per operation and the fresh-VM adapter at 74–98 microseconds.
The additional sub-millisecond orchestration overhead is real; no model or
network round-trips were included, so these numbers **do not** prove overall
latency or token savings. The expected benefit requires multi-step MCP tasks
with intermediate results filtered before reaching the model.

This prototype is a starting implementation, **not** a finished security
boundary or a faithful replacement for Codex's Code Mode host.
