# miodesk Edit and Search Budgets Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make edit/diff work, recent edit history, builtin search, and new-file permissions bounded and truthful.

**Architecture:** Reject oversized edit content before commit; generate a bounded diff with an explicit truncation bit; keep only a byte-bounded recent diff cache; report builtin search limits through the existing `Truncated` result field; preserve atomic replacement while using umask-filtered permissions for new files.

**Tech Stack:** Go standard library, existing `internal/tools` workspace layer, existing server edit-history cache and HTTP access middleware.

**Spec:** `docs/superpowers/specs/2026-09-23-miodesk-maintenance-design.md`

## Global Constraints

- Existing and resulting edited files are each capped at 2 MiB; aggregate old/new edit bytes per batch are capped at 16 MiB.
- Keep the existing 100-operation edit batch cap and all-or-nothing file commit behavior.
- Limit generated diff groups to 20,000 serialized diff lines and 1 MiB of JSON-encoded diff data per batch; report `diff_truncated: true` when abbreviated.
- Keep no more than 10 edit-history records or 2 MiB of cached diff data, whichever limit is reached first.
- Search must set `Truncated=true` if it hits a byte cap or scanner error, even below the match-count limit.
- New files use 0666 filtered by process umask; overwrites preserve existing permission bits.
- Do not add dependencies, weaken workspace canonical-path checks, or log file contents.

## Review Focus

- A file with hundreds of thousands of newline-only lines must be summarized before per-line diff allocations grow.
- A small edit batch can exceed aggregate limits through many individually valid files and must remain atomic on rejection.
- Ten large recent edit records must never push retained diff bytes above 2 MiB.
- A selected file or directory containing data after the 1 MiB fallback boundary must not return `Truncated=false` when it was not fully scanned.
- Creating a file under umask 077 must not force group/world permissions; overwriting an existing 0600 file must retain 0600.

## File Structure

- `internal/tools/edit.go` and `diff.go`: enforce content budgets before file commit and line allocation.
- `internal/tools/write.go`: preserve atomic write behavior with umask-filtered create permissions.
- `internal/tools/search.go`: propagate incomplete-scan state through the existing output shape.
- `internal/server/server.go`: bound and evict recent edit snapshots before exposing `/api/edits`.
- Existing tools/server tests own boundary cases. `docs/SECURITY.md` changes are kept in the context/status plan so the shared security page has one owner.

---

### Task 1: Enforce per-file and aggregate edit-content budgets

**Files:**
- Modify: `internal/tools/edit.go`
- Test: `internal/tools/write_test.go`

**Interfaces:**
- Keep `MaxEditBytes = 2 << 20` as both the old-file and new-file per-file limit.
- Add `MaxEditBatchBytes = 16 << 20` for the sum of old and new content across all paths in the batch.
- Keep `Edit(ctx, ws, in) (*EditOutput, error)` and commit only after all budgets pass.

- [x] **Step 1: Add per-file output-limit and atomicity tests**

Add `TestEditRejectsOversizedNewContentWithoutWriting`: write `original`, request a replace whose new content is `strings.Repeat("x", MaxEditBytes+1)`, assert an error and assert the file still equals `original`.

Add `TestEditRejectsAggregateBatchBytesWithoutWriting`: create five files each at `MaxEditBytes`, request one valid one-byte edit per file, assert the total old/new budget error and verify all five files retain their original bytes.

- [x] **Step 2: Run these tests and confirm the current implementation fails**

Run: `go test ./internal/tools -run 'TestEditRejectsOversizedNewContentWithoutWriting|TestEditRejectsAggregateBatchBytesWithoutWriting' -count=1`

Expected: FAIL because only existing file sizes are capped.

- [x] **Step 3: Check new content after every operation and before commit**

After each `applyOperation`, reject when `len(content) > MaxEditBytes`. Track `len(old)+len(new)` across prepared paths and reject once it exceeds `MaxEditBatchBytes`. Perform these checks during phase 1, before `commitEdits`.

- [x] **Step 4: Re-run edit atomicity tests and commit**

Run: `go test ./internal/tools -run 'TestEdit|TestWrite' -count=1`

Expected: PASS, including existing multi-file rollback tests. Commit with message `fix: cap edit content before atomic commit`.

### Task 2: Generate bounded diffs before committing edits

**Files:**
- Modify: `internal/tools/diff.go`
- Modify: `internal/tools/edit.go`
- Test: `internal/tools/diff_test.go`
- Test: `internal/tools/write_test.go`

**Interfaces:**
- Add `func DiffLinesBounded(oldText, newText string, maxLines, maxBytes int) ([]DiffGroup, bool)`; the boolean is `true` when the output is abbreviated.
- Keep `DiffLines(oldText, newText string) []DiffGroup` as a compatibility wrapper using the standard diff limits.
- Add `DiffTruncated bool` with JSON name `diff_truncated` to `EditFileResult`.

- [x] **Step 1: Add bounded-diff behavior tests**

Add `TestDiffLinesBoundedStopsBeforeSplittingDenseInput`: call `DiffLinesBounded(strings.Repeat("\n", 20001), "changed\n", 20000, 1<<20)` and assert no more than the serialized diff-line/JSON-byte budget is returned and `truncated` is true. Add `TestDiffLinesBoundedCapsJSONEscapes`: make the added line contain 200,000 `<` characters plus quotes, backslashes, and control characters; marshal the returned groups and assert `truncated` is true and `len(jsonBytes) <= 1<<20` for a `1<<20` byte cap, proving JSON escaping cannot exceed the serialized limit.

Add `TestEditReportsTruncatedDiffAndStillCommitsAtomically`: in one two-file batch, make a dense edit that exceeds the diff budget plus a small valid edit; assert both new file contents are installed, `DiffTruncated` is true for the abbreviated file, and the edit call succeeds without a partially committed batch.

- [x] **Step 2: Run the tests and confirm the current implementation fails**

Run: `go test ./internal/tools -run 'TestDiffLinesBoundedStopsBeforeSplittingDenseInput|TestDiffLinesBoundedCapsJSONEscapes|TestEditReportsTruncatedDiffAndStillCommitsAtomically' -count=1`

Expected: FAIL because `DiffLines` always materializes line operations and there is no truncation field.

- [x] **Step 3: Add cheap preflight and bounded output construction**

Before `strings.Split`, count line separators and compare the old/new totals with `maxLines`; include each emitted group header and diff line in the serialized line count. If over budget, return `nil, true`. During group construction, use a bounded JSON-string size counter that follows `encoding/json` escaping, include array/group separators and field names, and stop before exceeding `maxBytes`; return the groups already built and `true`. Keep the current LCS cell cap inside the bound. The byte cap applies to the serialized `Diff` groups across the whole batch; file path/byte/line summary fields remain available when a diff is truncated.

- [x] **Step 4: Compute diff budgets in edit phase 1**

For each prepared file, call `DiffLinesBounded` before `commitEdits`, passing the remaining batch line and byte budgets. Store groups and `DiffTruncated` on its prepared result. If a file exhausts the batch budget, mark later changed files truncated with no added groups. Commit only after all edit and diff result work is prepared.

- [x] **Step 5: Add and run the dense-diff benchmark**

Add `BenchmarkDiffLinesBoundedDense` to `diff_test.go`. Construct old and new 20,001-line inputs before `b.ResetTimer()`, call `b.ReportAllocs()`, then call `DiffLinesBounded` for each iteration and assert the result is truncated. Run `go test ./internal/tools -run '^$' -bench '^BenchmarkDiffLinesBoundedDense$' -benchmem -benchtime=5x` and confirm the benchmark reports allocations and completes successfully.

- [x] **Step 6: Run diff and transaction tests and commit**

Run: `go test ./internal/tools -run 'TestDiff|TestEditBatchIsAtomic|TestEditMultiFileCommit|TestEditReportsTruncatedDiff' -count=1`

Expected: PASS; dense input remains bounded and edit atomicity is unchanged. Run `git diff --check`; commit the four changed paths with message `fix: bound edit diff generation`.

### Task 3: Bound recent edit history and correct its exposure contract

**Files:**
- Modify: `internal/server/server.go`
- Modify: `internal/server/security_test.go`
- Modify: `internal/server/server_test.go`

**Interfaces:**
- Keep `recordEdits([]tools.EditFileResult)` and `recentEdits() []EditRecord`.
- Add `maxRecentEditBytes = 2 << 20`; preserve the existing 10-record maximum.
- `/api/edits` remains behind `s.auth.Middleware` and returns only in-memory recent edits.

- [x] **Step 1: Add retention and access-mode tests**

In `server_test.go`, record 11 results containing known diff text and assert the oldest record is evicted. Add enough diff text to cross 2 MiB and assert the retained JSON-relevant diff bytes never exceed the budget. Add `TestRecordEditsDoesNotMutateToolResultWhenTruncatingCache`: record a single result larger than the cache cap, assert the cached copy retains only path/byte/line summary with `Diff=nil` and `DiffTruncated=true`, and assert the original tool result still contains its full diff. In `security_test.go`, add `TestEditsEndpointFollowsAccessMode`: request `/api/edits` without a token and assert 401, then request with the valid test token and assert the response contains the test edit text. Also verify a same-origin local-mode request and an unsafe-mode request without credentials return 200, documenting existing policy rather than changing it.

- [x] **Step 2: Run the tests and confirm the history budget is missing**

Run: `go test ./internal/server -run 'TestRecentEdit|TestEditsEndpointFollowsAccessMode' -count=1`

Expected: FAIL on byte retention; the existing record count may already pass.

- [x] **Step 3: Enforce a total byte budget when recording**

Estimate retained diff bytes from each `DiffGroup.Header`, each `DiffLine.Text`, and fixed structural overhead. Evict oldest records until both `len(s.edits) <= 10` and estimated bytes `<= 2<<20`. If a record alone exceeds the history budget, retain its path/byte/line summary, clear its `Diff`, and set `DiffTruncated=true`.

Add `BenchmarkRecordEditsBounded` to `server_test.go`; prepare one edit result at the 1 MiB output cap before the timer, repeatedly record it into a fresh `Server`, call `b.ReportAllocs()`, and assert retained bytes stay within 2 MiB.

- [x] **Step 4: Run the retention benchmark and focused tests**

Run `go test ./internal/server -run '^$' -bench '^BenchmarkRecordEditsBounded$' -benchmem -benchtime=5x` and `go test ./internal/server -run 'TestRecentEdit|TestRecordEditsDoesNotMutateToolResultWhenTruncatingCache|TestEditsEndpointFollowsAccessMode|TestMiddlewareTokenMode' -count=1`.

Expected: both commands pass; benchmark reports allocations and the access-mode tests match the existing local/token/unsafe policy.

- [x] **Step 5: Commit the retention and access-mode change**

Run `git diff --check`; commit the three changed paths with message `fix: cap recent edit history`.

### Task 4: Report builtin search truncation accurately

**Files:**
- Modify: `internal/tools/search.go`
- Test: `internal/tools/tools_test.go`

**Interfaces:**
- Change `searchOneFile` to `searchOneFile(...) ([]Match, bool)`, where the boolean signals an incomplete scan.
- Keep `SearchOutput.Truncated bool` as the model-facing completeness signal.

- [x] **Step 1: Add selected-file and directory fallback tests**

Extend `TestSearchBuiltin` with a file larger than 1 MiB whose match appears after the fallback boundary, and a directory containing both a large skipped file and an ordinary file. Force the builtin path using the test hook. Assert `Truncated` is true even when returned matches are fewer than `MaxResults`.

Add an overlong-line case that triggers `bufio.Scanner` failure and assert `Truncated` is true.

- [x] **Step 2: Run the tests and confirm silent truncation**

Run: `go test ./internal/tools -run '^TestSearchBuiltin$' -count=1`

Expected: FAIL because incomplete scans currently return only matches.

- [x] **Step 3: Propagate byte-cap and scanner status**

Have `searchOneFile` return `(matches, truncated)`. Set `truncated` when the file is skipped by `sizeCap`, when its size exceeds the `LimitReader` boundary, when `os.Open`/`Stat` prevents a scan, or when `sc.Err() != nil`. `searchBuiltin` ORs that value into `SearchOutput.Truncated` for both file and directory paths while retaining the match-count limit behavior.

- [x] **Step 4: Run builtin and ripgrep parity tests and commit**

Run: `go test ./internal/tools -run 'TestSearchBuiltin|TestSearchRipgrep|TestSearchRespectsSandbox' -count=1`

Expected: PASS. Commit with message `fix: report builtin search truncation`.

### Task 5: Respect umask for newly created files

**Files:**
- Modify: `internal/tools/write.go`
- Test: `internal/tools/write_test.go`
- Create: `internal/tools/write_mode_unix_test.go`

**Interfaces:**
- Keep `writeFileAtomic(path string, data []byte) error`.
- Preserve existing file mode on overwrite; create new staged files with mode `0666` so the OS applies process umask.

- [x] **Step 1: Add an isolated Unix umask test**

Create `write_mode_unix_test.go` with build tag `//go:build aix || android || darwin || dragonfly || freebsd || illumos || ios || linux || netbsd || openbsd || solaris`. In a non-parallel test, set `old := syscall.Umask(0o077)`, restore `old` with `defer`, create a new file through `writeFileAtomic`, and assert `Mode().Perm() == 0o600`. Also create a 0600 file, replace it, and assert it remains 0600.

- [x] **Step 2: Run the Unix mode test and confirm the new-file expectation fails**

Run: `go test ./internal/tools -run '^TestWriteFileAtomicHonorsUmask$' -count=1`

Expected: FAIL because the current code chmods new files to 0644.

- [x] **Step 3: Create a collision-safe staged file using umask-filtered mode**

Use `crypto/rand` for a short random suffix, `os.OpenFile` with `O_CREATE|O_EXCL|O_WRONLY` and mode `0666`, retry at most 10 `os.ErrExist` collisions, then preserve the existing sync/close/rename/cleanup sequence. Do not read or mutate the process umask.

- [x] **Step 4: Run all write/edit tests and commit**

Run: `go test ./internal/tools -run 'TestWrite|TestEdit' -count=1`

Expected: PASS on the current platform; existing mode bits survive overwrite and new-file permissions follow umask. Commit with message `fix: respect umask for new workspace files`.

### Task 6: Verify the edit/search workstream

- [x] Complete the focused race gate.

Run: `go test -race ./internal/tools ./internal/server`

Expected: PASS. Confirm the boundary tests show no partial edit after rejection, no silent builtin-search truncation, and no cached diff data beyond the configured budget.
