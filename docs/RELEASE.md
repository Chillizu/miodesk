# Release checklist

The public install script downloads the latest **tagged GitHub Release**,
not the tip of `main`. Release `v0.2.2` introduces experimental, default-off
Code Mode and the streamlined local-first onboarding. Each release needs a
clean, tested Git commit, a reviewed tag, versioned assets and successful
public download verification. Run from a normal clone of
`github.com/Chillizu/miodesk`.

## Before tagging

1. Keep the repository's `MPL-2.0` license in `LICENSE` and review it before
   publishing. MPL-2.0 permits commercial use while requiring covered source
   files and modifications to remain available under the license.
2. Review `git status --short` and keep only intentional source, test, and
   documentation changes.
3. Remove any accidentally tracked local build outputs such as root-level
   `miodesk`, `miodesk-audit`, or `dist/*`. They are machine-specific and must
   not be part of the release commit.
4. Confirm that no runtime key, tunnel-client profile, local config, journal
   export, or workspace data is tracked.
5. Run the read-only preflight:

   ```sh
   scripts/release-check.sh
   scripts/install-test.sh
   bash scripts/onboarding-test.sh
   ```

The preflight intentionally fails when tracked build outputs are present. It
also runs `go vet`, the race-enabled test suite, and a clean binary build.

## Build the release assets

From a clean, reviewed tree:

```sh
scripts/build-release.sh 0.2.2
```

The script creates these five binaries under `dist/`:

```text
miodesk-linux-amd64
miodesk-linux-arm64
miodesk-darwin-amd64
miodesk-darwin-arm64
miodesk-windows-amd64.exe
```

It also copies the verified-release installer to `dist/install.sh` and creates
`dist/checksums.txt` plus `dist/manifest.json`. The checksum file covers the
platform binaries and installer. The manifest
uses the GitHub release URLs for `Chillizu/miodesk`; override the repository
only when publishing a deliberate fork:

```sh
REPOSITORY=owner/repository scripts/build-release.sh 0.2.2
```

Inspect the manifest and checksums before uploading. The update command uses
the manifest's platform asset and verifies its SHA-256 before replacing the
current binary atomically:

```sh
miodesk update --from https://github.com/Chillizu/miodesk/releases/download/v0.2.2/manifest.json --check
```

## Tag and publish

Only after the review above, the maintainer can create and push the release:

```sh
git tag -a v0.2.2 -m "miodesk v0.2.2"
git push origin main
git push origin v0.2.2
```

Create a GitHub release for `v0.2.2`, upload every `dist/miodesk-*` file,
`dist/install.sh`, `dist/checksums.txt`, and `dist/manifest.json`, then run the update check from
a separate test installation. Do not upload the runtime key or a local
`tunnel-client` profile.

## Release scope

The release should communicate these stable user-facing decisions:

- New-user shell install + PATH instructions work on supported platforms;
  local-only `setup` requires no OpenAI credentials and should work unattended
  when stdin is not a TTY;
- The README version notice must match the latest published GitHub Release
  after publishing, including the experimental Code Mode disclaimer;
- fresh setup defaults to a loopback MCP server; OpenAI Tunnel is opt-in;
- `miodesk setup` configures local paths and only touches the external
  tunnel-client profile when explicitly asked; `miodesk tunnel setup` refreshes
  a previously configured profile without restarting Core;
- local tools remain Native-first in ChatGPT (no MCP Apps widget or template);
- legacy connection configurations remain readable, while the primary CLI
  does not enumerate alternative tunnel providers; and
- logs are structured and correlate requests without recording workspace
  contents, command lines, authorization headers, or tokens.
