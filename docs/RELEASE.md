# Release checklist

The public installer downloads the latest **published GitHub Release**, not the
tip of `main`. This checklist applies to future versions; historical release
notes and artifacts remain available from
[GitHub Releases](https://github.com/Chillizu/miodesk/releases).
The release tag is immutable: **do not rewrite an already published tag**.

## 1. Decide the version and verify source

Set a new version before following the commands below. For example, after
`v0.2.2`, the next patch release could be `0.2.3`.

```sh
VERSION=0.2.3
git status --short --branch
git fetch origin
git diff --exit-code
git rev-parse HEAD
go test -vet=all ./...
scripts/release-check.sh
bash scripts/onboarding-test.sh
bash scripts/codemode-smoke/ci.sh
```

Confirm `main` is clean, synchronized with `origin/main`, and GitHub CI
passes on the **final release commit**. If it adds code or changes user-visible
behavior, update `CHANGELOG.md`, `README.md` and `docs/CODEMODE.md` when
applicable. Review `LICENSE` and confirm no credentials, local tunnel
profiles, binary dumps or account-specific `.app.json` files are tracked.

Never upload local API keys, `config.toml`, `server.json` or service logs.
The repository contains only the account-neutral
`plugins/miodesk/.app.example.json`.

## 2. Build and verify release assets

```sh
scripts/build-release.sh "$VERSION"
(cd dist && sha256sum -c checksums.txt)
./dist/miodesk-linux-amd64 version  # when running on Linux amd64
```

`dist/` contains binaries for Linux amd64/arm64, macOS amd64/arm64 and
Windows amd64, plus `install.sh`, `checksums.txt` and `manifest.json`.
The scripts do not publish anything. Cross-compiled binaries are compile
checked, but hardware-specific native testing is still recommended.

Test the real candidate installer in an isolated directory using
`MIODESK_RELEASE_BASE=file://$(pwd)/dist` and a temporary
`MIODESK_BIN_DIR`. Keep existing local service and tunnel installations
untouched during release validation.

## 3. Tag and publish

After reviewing the build and preparing Markdown release notes (from the
changelog, including clear **Experimental** caveats for Code Mode):

```sh
git tag -a "v$VERSION" -m "miodesk v$VERSION"
git push origin main
git push origin "v$VERSION"
gh release create "v$VERSION" \
  dist/miodesk-* dist/install.sh dist/checksums.txt dist/manifest.json \
  --repo Chillizu/miodesk --verify-tag \
  --title "miodesk v$VERSION" \
  --notes-file /path/to/reviewed-release-notes.md --latest
```

Choose an actual, reviewed notes file before invoking `gh release create`.
Creating the tag/release is an explicit maintainer decision, not a side
effect of `scripts/build-release.sh`.

## 4. Verify the **published** release

1. Check the GitHub Release is public, latest (when appropriate), and
   contains all eight assets.
2. Compare each uploaded asset's GitHub SHA-256 digest and size with
   `dist/`. Check `checksums.txt` by an actual public download.
3. Re-run the default `scripts/install.sh` **without** any release-base
   override in a disposable installation directory, then perform a fresh
   `miodesk setup --workspace /absolute/path` and `miodesk doctor`.
4. Check the downloaded binary reports the new version and commit; run the
   MCP smoke on the candidate if necessary.
5. Refresh the README version claims and any separately packaged plugin
   manifests. A GitHub Release does **not** automatically republish a ChatGPT
   plugin or refresh a registered MCP action schema.

Keep `miodesk.service` and `miodesk-tunnel.service` separate. Never restart
or rewrite a user's tunnel credentials as part of a repository release.
