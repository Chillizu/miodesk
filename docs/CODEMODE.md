# Code Mode (Experimental · Linux)

Code Mode was introduced in miodesk **v0.2.2** as an experimental feature.
It is **disabled by default**, its tool API and limits may change, and it is
not a security boundary for code provided by unrelated untrusted parties.

## Enable and use

Code Mode is an **opt-in feature of miodesk Core**, not a tunnel or
separate daemon. Existing configurations expose exactly the original ten tools.
On Linux, an administrator can add the following to `config.toml` and restart
only the Core service:

```toml
[codemode]
enabled = true
```

With this setting, Core advertises an additional **read-only** MCP `code`
tool. OpenAI Secure MCP Tunnel remains an independent transport process and
still forwards traffic to the same local MCP server. Enabling or disabling
Code Mode never changes tunnel profiles, credentials, or service files. A
ChatGPT connector may require a tool-list refresh after opt-in.

This implementation is inspired by Codex's code-composition interface and
Cloudflare's Code Mode approach. It is not Codex's V8 host or its `exec` /
`wait` protocol: we intentionally use a small fresh Goja runtime per call.

## Example

```js
async () => {
  const hits = await tools.search({query: "TODO", path: "project", max_results: 10});
  const details = [];
  for (const hit of hits.matches) {
    const text = await tools.read({path: hit.file, offset: hit.line, limit: 1});
    details.push({file: hit.file, line: hit.line, content: text.content.trim()});
  }
  return details;
}
```

The script is the value of the `source` argument for the `code` tool.
The MCP result has the structure `{ "kind": "code", "value": ..., "calls": N }`:
`value` may be any JSON-compatible object, array, string, number, boolean or
`null`. Scripts should return a **small, filtered result**, not a complete
file or search transcript. JavaScript exceptions are reported as tool errors;
their user-supplied text is not copied into the persistent Core logs.

The runtime accepts standard JavaScript async arrow functions with immediate
native-tool dispatch. It does not supply Node.js APIs, a module loader,
persistent sessions, the Codex V8 `exec`/`wait` lifecycle or delayed external
async operations. All state is discarded after each call.

Only `tools.read`, `tools.search`, and `tools.list` are available to scripts.
Core dispatches each call to the existing validated workspace tools. The
script never sees native Go tool pointers, arbitrary MCP tool names, OS file
descriptors, the shell, Node.js modules, provider credentials, or network APIs.

## Execution boundaries (Linux)

1. Code execution runs in a **fresh child process of the miodesk executable**,
   not in the long-running Core process. The child is launched using a hidden
   internal command, with a cleared environment, neutral working directory and
   private stdin/stdout pipes.
2. The child has Linux `RLIMIT_DATA = 128 MiB` and `RLIMIT_CORE = 0` resource
   limits. A separate Core-side deadline kills the child even if JavaScript
   cancellation fails. Concurrent Code Mode requests are limited to two workers.
3. The parent Core alone resolves workspace paths and executes allowlisted
   read-only operations. JSON line protocol frames are size-limited and must
   follow the expected ordered request-response sequence.
4. Script source is limited to 16 KiB; a script may invoke at most 16 tools;
   intermediate responses total at most 512 KiB, final result at most 32 KiB;
   JavaScript execution is bounded to 2 seconds and the full child lifetime
   to 4 seconds. Read/search/list have stricter per-operation caps.
5. The `code` MCP tool is absent unless explicitly enabled. There is no
   implicit privilege elevation and no change to the `exec_command` surface.

**Security boundary:** this is process/resource isolation and an
API allowlist, **not** a complete container, mount namespace, seccomp, or
unprivileged-user filesystem sandbox. The worker runs as the same Unix user
as the Core; a native engine exploit could escape JavaScript's API surface.
Consequently the feature is opt-in and read-only, not suitable for scripts
from unrelated untrusted principals without a stronger OS boundary.

On non-Linux platforms, `codemode.enabled = true` is currently rejected by
Core startup validation; other platform builds still compile with the feature
disabled. This avoids silently advertising unsupported memory protection.

## Verification

The tests exercise composed tool calls, no write/exec/network Node.js APIs,
workspace-path escape, memory exhaustion, infinite loops, source/output/call
limits, cancellation, configuration opt-in, the MCP tool schema, numeric /
boolean / array / null JSON results, and an isolated Streamable HTTP client
roundtrip. A regression test ensures JavaScript exception strings never enter
persistent Core tool logs. CI includes Go test/race builds
and `bash scripts/codemode-smoke/ci.sh`, which starts a disposable enabled
server and exercises the real MCP `code` tool.

Local dispatch benchmark of the initial unisolated prototype: a direct
`list → read` sequence cost 13.5–25.5 microseconds; fresh-JS-VM composition
cost 74–98 microseconds in two short 100-iteration runs on an Intel Ultra 9
185H. These are *not* end-to-end or token measurements. The isolated worker
adds process startup and IPC, so any model-side savings must be validated
against real client tool-call workflows.

A future version may support more than three tools, but only after explicit
read/write authorization design. Existing tools should remain independently
available; keeping the Code Mode option does not require making every user
use it.
