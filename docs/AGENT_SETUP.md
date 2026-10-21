# Agent-assisted miodesk setup (English)

**Purpose:** This is the canonical, web-readable, step-by-step runbook for an AI
assistant helping a new user install miodesk **from an ordinary ChatGPT chat**,
before the assistant has access to the user's computer. The Chinese version is
[AGENT_SETUP.zh-CN.md](AGENT_SETUP.zh-CN.md). The public project is
[Chillizu/miodesk](https://github.com/Chillizu/miodesk).

## Instructions for the assistant

You are a **guide, not a connected terminal**. Reading a web page cannot run
shell commands on the user's computer or configure their ChatGPT account. At
each checkpoint, give **one complete, OS-specific copyable code block** that
performs that checkpoint and reports its status; do not scatter a single step
across many unrelated snippets or make users manually edit configuration
files. Explain prerequisites in one or two sentences, let the user run the
block, then inspect **redacted** output before presenting the next. Ask for
manual web UI actions only when there is no supported CLI (e.g. Platform
Tunnel creation and ChatGPT app approval). Never claim a command ran or
ChatGPT is connected until evidence proves it.

**Conversation sequence (one stage at a time):**

1. **Ask for OS and architecture** (Windows, macOS or Linux; x64/ARM64), and
   whether the user already has miodesk installed. Give a read-only OS check
   if they do not know; do not guess from the chat's device or network location.
2. **Ask the goal**: (A) local MCP with a local coding client; (B) private
   ChatGPT access using official OpenAI Secure MCP Tunnel; or (C) explore first.
   Also ask what **existing directory** should be shared. Explain that file
   tools are workspace-scoped but `exec_command` runs with OS user privileges.
   Recommend a small project directory, **never the whole home drive by
   default**.
3. **Verify prerequisites before installing**: check whether the system and
   architecture have an official release asset, whether the user can run a
   terminal/PowerShell, and whether ChatGPT custom MCP access is actually
   available in the target plan/workspace. If ChatGPT's tunnel option or
   permissions are missing, explain the blocker; do not advise unsafe public
   exposure or pretend the tunnel is available.
4. **Install Core only** from the official versioned GitHub Release with
   checksum verification. Offer commands below for that OS, ask the user to
   confirm before running downloaded code, and request `miodesk version`
   (or its full Windows path). Never ask them to paste API keys.
5. **Configure the local workspace without interactive prompts**, then run
   `miodesk doctor --json`. From `v0.2.2`, the stable syntax is
   `miodesk setup --workspace <absolute-existing-directory>`. Newer builds
   may also support `--non-interactive`; check `miodesk setup -h` before using
   it. If available, run `miodesk preflight --json` for a secret-free local
   environment report. Do not use either new flag on a release that lacks it.
6. **Start and verify local Core** with `miodesk serve` in a dedicated
   terminal, not a background service yet; confirm `/healthz` returns OK
   and/or `miodesk status` reports healthy from another terminal. Don't start
   two servers on the same port.
7. **Branch at the first working local MCP checkpoint:**
   - For local MCP clients, configure stdio (`serve --stdio`) or loopback
     HTTP. Do not start both modes unnecessarily. Verify a safe `status` call.
   - For ChatGPT, continue to the private Tunnel stage below. **Local
     readiness does not mean ChatGPT can reach the service.**
8. **Complete the ChatGPT connection only if authorized/available**: guide
   through Platform tunnel permissions, workspace associations, official
   `tunnel-client` installation, secure local key file, `miodesk setup
   --tunnel-id ... --runtime-key-file <file>`, tunnel health and readiness,
   then ChatGPT Plugins → Add custom MCP server → Tunnel. Verify the real
   `status` tool **from ChatGPT**, not merely a local HTTP 200.
9. **Only after a successful model-side call**, offer optional persistent
   lifecycle setup. Linux/systemd users may install/start **independent**
   `miodesk.service` and `miodesk-tunnel.service`; verify any pre-existing
   units and credential files before changing them. On macOS/Windows, prefer
   foreground operation first; do not invent supported service commands.
   Experimental Code Mode is Linux-only, default-off, and **not** a setup
   prerequisite.
10. **Handoff**: once `@miodesk status` or an equivalent genuine model-side
    tool call succeeds, explain that later changes (project settings, scripts,
    bounded workspace operations, tests) can be performed **through the
    connected miodesk tools** with user permission. Never claim that the web
    chat automatically acquired those tools.

**Safety rules:** ask one question at a time when possible; adapt to the
user's actual replies rather than dumping every command. Commands must be
literal and copyable, not pseudocode masquerading as a working one-liner.
Never request passwords, runtime API keys, access tokens, full config files
containing secrets or private tunnel profiles in chat. Have users redact
IDs/tokens if unsure; a tunnel ID may also be kept private. Do not run
unreviewed remote shell scripts, change `sudo`/UAC/system policy, enable
`remote.mode=unsafe`, disable firewalls, or expose `0.0.0.0` as a
workaround. Never silently edit or overwrite a running tunnel/systemd unit.

## Copyable system checks (before installing)

**Windows PowerShell (read only):**

```powershell
[System.Environment]::OSVersion.VersionString
[System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture
$PSVersionTable.PSVersion.ToString()
```

**macOS / Linux terminal (read only):**

```sh
uname -s
uname -m
```

Official `v0.2.2` assets: Linux x64 / arm64, macOS x64 / arm64,
Windows **x64**. Windows ARM64 has no native official artifact; do not
silently assume that Windows x64 emulation is supported. Unsupported systems
should stop and review the [release assets](https://github.com/Chillizu/miodesk/releases).

## Install and local verification

**Linux / macOS:**

```sh
# Review the script before executing it:
curl -fsSL https://raw.githubusercontent.com/Chillizu/miodesk/main/scripts/install.sh -o miodesk-install.sh
sh miodesk-install.sh
export PATH="$HOME/.local/bin:$PATH"
miodesk version
```

The shell installer verifies SHA-256, doesn't install the OpenAI client or
start a daemon, and prints the destination binary path. For a pre-existing
binary check for a running service **before replacing it**.

**Windows x64 (PowerShell):**

```powershell
$installer = Join-Path $env:TEMP "miodesk-install.ps1"
Invoke-WebRequest "https://raw.githubusercontent.com/Chillizu/miodesk/main/scripts/install.ps1" -OutFile $installer
# Inspect the downloaded script in an editor, then:
& $installer
& "$env:LOCALAPPDATA\Programs\miodesk\miodesk.exe" version
```

The PowerShell installer verifies the release binary using SHA-256 and does
not change PATH, policy, startup entries or services. If execution policy
blocks the script, report the exact error; don't ask the user to disable
system protections. Windows users should use the full binary path from the
installer until they voluntarily configure PATH.

**Local-only setup** (replace with a trusted, existing project folder):

```sh
miodesk setup --workspace /absolute/path/to/project
miodesk doctor --json
miodesk serve
```

For Windows PowerShell use the executable's full path, for example:

```powershell
$miodesk = "$env:LOCALAPPDATA\Programs\miodesk\miodesk.exe"
& $miodesk setup --workspace "C:\path\to\project"
& $miodesk doctor --json
& $miodesk serve
```

The extra `--non-interactive` switch and `preflight --json` are available on
newer development builds; **not all published versions have them**. Always
check `setup -h` and `help` when guiding users of an older Release.
On first setup, the workspace folder must already exist; pick a fixed
loopback port (default 8787), rather than `--port 0` for Tunnel.

With Core running, check from a **second terminal**:

```sh
miodesk status
```

For Windows: `& $miodesk status`. The local HTTP health URL is
`http://127.0.0.1:8787/healthz`. No OpenAI connection is implied by this
check.

## ChatGPT-only stage: official Secure MCP Tunnel

Official current documentation:
[OpenAI Secure MCP Tunnel](https://developers.openai.com/api/docs/guides/secure-mcp-tunnels)
and [ChatGPT MCP apps](https://help.openai.com/en/articles/12584461-developer-mode-and-full-mcp-connectors-in-chatgpt).
Consult those **live** before describing Platform/ChatGPT UI controls or
permissions. Do not fabricate menu labels or assume all plans can add
private custom MCP plugins.

1. In **Platform tunnel settings**, obtain a tunnel ID, create/restrict a
   runtime key and associate the correct Platform organization **and**
   ChatGPT workspace. Creating/editing tunnels requires Tunnels Read + Manage;
   using them requires Tunnels Read + Use. Workspace app permissions are
   separate. The user completes sign-in and any account approval.
2. Install the official OpenAI `tunnel-client` from its current release
   link / Platform UI, not from an unverified mirror. Confirm
   `tunnel-client help quickstart` is available.
3. **One copyable terminal block:** ask the user to paste **the block for their OS below** into their own terminal. It interactively asks for the Tunnel ID, accepts the runtime API key **without echo**, writes it to a private local file (Unix mode 0600 / Windows user-only ACL), and invokes existing `miodesk setup` with **only the ID and key-file path**. No key content should enter the chat, command history, or miodesk config.toml. The blocks refuse to replace an existing key file; for key rotation, stop and plan that separately. The Tunnel must already exist and the official tunnel-client must already be installed. The commands do not install services or restart anything.

   **Linux / macOS (one block):**

   ```bash
   # Paste this whole block into a local Linux/macOS terminal, after installing
   # the official tunnel-client and creating the Tunnel in OpenAI Platform.
   bash <<'BASH'
   set -euo pipefail
   command -v miodesk >/dev/null || { echo 'Install miodesk first.'; exit 1; }
   command -v tunnel-client >/dev/null || { echo 'Install the official tunnel-client first.'; exit 1; }
   key_dir="${XDG_CONFIG_HOME:-$HOME/.config}/miodesk"
   key_file="$key_dir/openai-runtime-key"
   umask 077
   mkdir -p "$key_dir"
   chmod 700 "$key_dir"
   if [[ -e "$key_file" || -L "$key_file" ]]; then
     echo "Key file already exists: $key_file (will not overwrite it)."
     exit 1
   fi
   read -r -p 'Tunnel ID (tunnel_...): ' tunnel_id </dev/tty
   [[ "$tunnel_id" == tunnel_* ]] || { echo 'Invalid Tunnel ID.'; exit 1; }
   read -r -s -p 'Runtime API key (hidden): ' runtime_key </dev/tty
   printf '\n' >/dev/tty
   [[ -n "$runtime_key" ]] || { echo 'Empty runtime key.'; exit 1; }
   if ! (set -C; printf '%s' "$runtime_key" > "$key_file"); then
     unset runtime_key
     echo 'Could not safely create the key file.'; exit 1
   fi
   unset runtime_key
   chmod 600 "$key_file"
   miodesk setup --tunnel-id "$tunnel_id" --runtime-key-file "$key_file"
   miodesk doctor
   BASH
   ```

   **Windows x64 PowerShell (one block):**

   ```powershell
   # Paste this entire block into Windows PowerShell on your own computer.
   & {
     $ErrorActionPreference = 'Stop'
     $miodesk = Join-Path $env:LOCALAPPDATA 'Programs\miodesk\miodesk.exe'
     if (!(Test-Path -LiteralPath $miodesk)) { throw 'Install miodesk first.' }
     if (!(Get-Command tunnel-client -ErrorAction SilentlyContinue)) { throw 'Install the official tunnel-client first.' }
     $configBase = if ($env:XDG_CONFIG_HOME) { $env:XDG_CONFIG_HOME } else { Join-Path $HOME '.config' }
     $keyDir = Join-Path $configBase 'miodesk'
     $keyFile = Join-Path $keyDir 'openai-runtime-key'
     New-Item -ItemType Directory -Path $keyDir -Force | Out-Null
     if (Test-Path -LiteralPath $keyFile) { throw "Existing key file: $keyFile (not overwritten)." }
     $stream = [IO.File]::Open($keyFile, [IO.FileMode]::CreateNew)
     $stream.Dispose()
     try {
       # Lock down the empty file BEFORE any secret bytes are written.
       $acl = [Security.AccessControl.FileSecurity]::new()
       $acl.SetAccessRuleProtection($true, $false)
       $sid = [Security.Principal.WindowsIdentity]::GetCurrent().User
       $rule = [Security.AccessControl.FileSystemAccessRule]::new(
         $sid, [Security.AccessControl.FileSystemRights]::FullControl,
         [Security.AccessControl.AccessControlType]::Allow)
       $acl.AddAccessRule($rule)
       Set-Acl -LiteralPath $keyFile -AclObject $acl -ErrorAction Stop
       $tunnelId = Read-Host 'Tunnel ID (tunnel_...)'
       if (!$tunnelId.StartsWith('tunnel_')) { throw 'Invalid Tunnel ID.' }
       $secure = Read-Host 'Runtime API key (hidden)' -AsSecureString
       $ptr = [Runtime.InteropServices.Marshal]::SecureStringToBSTR($secure)
       try {
         $plain = [Runtime.InteropServices.Marshal]::PtrToStringBSTR($ptr)
         if ([string]::IsNullOrWhiteSpace($plain)) { throw 'Empty runtime key.' }
         [IO.File]::WriteAllText($keyFile, $plain, [Text.UTF8Encoding]::new($false))
       } finally {
         [Runtime.InteropServices.Marshal]::ZeroFreeBSTR($ptr)
         $plain = $null
       }
       & $miodesk setup --tunnel-id $tunnelId --runtime-key-file $keyFile
       if ($LASTEXITCODE -ne 0) { throw 'miodesk setup failed. See the error above.' }
       & $miodesk doctor
     } catch {
       Remove-Item -LiteralPath $keyFile -Force -ErrorAction SilentlyContinue
       throw
     }
   }
   ```

4. The private profile is created by **the existing miodesk setup implementation** using the official tunnel-client; there is no separate `tunnel key set` command. Users who prefer an interactive wizard may instead run `miodesk setup --interactive` **after** creating their own private key file: it asks for workspace, port, Tunnel ID and key-file path, and must remain supported. If profile initialization fails, inspect only the **redacted** error and current official client's help; do not manually invent profile YAML or paste the secret into the conversation.
5. Run `tunnel-client run --profile miodesk` in a **separate** terminal while
   Core is running, or (only if no previous Core instance is running) use
   `miodesk connect` as the combined foreground mode. Confirm tunnel
   client readiness and active polling. The local MCP target stays loopback;
   no inbound port is needed.
6. In ChatGPT Plugins, create a **private custom MCP server** using
   Connection: Tunnel, select the intended tunnel, review auth/risk prompts,
   and configure access. Refresh the app's action list when necessary; new
   actions may initially be disabled. Request a real safe **`status`** tool
   call from ChatGPT and check the returned status and tool list.

**The checkpoint:** declare ChatGPT connection complete **only after** that
model-side MCP call succeeds. A healthy tunnel alone does not prove that
ChatGPT has the app available.

## After connection: optional persistence and improvements

Once ChatGPT can invoke miodesk, ask whether the user wants it to stay
available across logouts/reboots. On Linux with an available user systemd
manager:

```sh
miodesk service install
miodesk service start
miodesk tunnel service install
miodesk tunnel service start
```

These are **two independent units**, not one service. Before writing units,
check `systemctl --user cat miodesk.service miodesk-tunnel.service` for
existing configuration, especially `EnvironmentFile` and credentials.
Do **not** install a second copy of a running foreground Core/tunnel; migrate
deliberately with explicit user consent. On some Linux distributions, keeping
user services alive after logout requires separate user-manager settings;
don't enable lingering silently.

Windows and macOS do not have built-in miodesk `service` support. A
foreground terminal is an acceptable working setup. Discuss an external task
scheduler/service manager only later and only if the user specifically wants
it; never claim miodesk provisions one automatically.

After the model has a connected MCP tool, optional changes can be done using
`@miodesk` (where available) rather than walking the user through every
edit manually. Respect the existing workspace root, user permissions and
explicit side-effect approvals.

## When to stop

- Unsupported OS/architecture; missing ChatGPT plan/workspace permissions;
  unverified downloads; failed checksum; missing official tunnel client;
  inaccessible key file; unavailable outbound network; local port conflict;
  or failed remote MCP call.
- Explain **which checkpoint failed**, show the minimal non-secret diagnostic
  needed, and give one next safe action. Don't falsely report success.

The official [README](../README.md), [local device setup](SETUP.md),
[ChatGPT connection guide](CHATGPT.md), and [security model](SECURITY.md)
contain details for specific failures.
