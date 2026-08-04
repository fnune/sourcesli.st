# Project review, 2026-08-04

A review of the whole project: features and usability first, implementation quality second. Each finding carries a status; "fixed" means fixed in the commit series this file arrived in.

## Verdict

Strong skeleton and unusually good engineering discipline, but the product documentation is ahead of the code: REQUIREMENTS.md and decision record 0003 promise features that do not exist. What exists is small, clean, and well-tested. The main risks were two ETL robustness bugs and a set of silent spec gaps, addressed as listed below.

## Features and usability

### What works well

- The ISO chooser is the standout, matching its own claim of earning the most attention. Plain-language questions, graceful handling of impossible combinations (live on arm64 degrades to netinst with an explanation), checksum and signature verification instructions behind `<details>`, and the firmware-since-Debian-12 note. Better than debian.org's download UX.
- No-JS-first is real: the search form has a real action and method, disclosure uses `<details>`, htmx only upgrades. Screen-reader-only labels are present.
- Staleness honesty: the footer shows "data as of" and a "no data loaded yet" state, the bug page links the BTS for live state, and the 404 copy explains that a bug may be newer than the snapshot. Calm, accurate copy throughout.

### Findings

| # | Finding | Status |
|---|---------|--------|
| F1 | Package page promises dependencies; the query fetched them but the template never rendered them. | Fixed: dependencies section on the package page. |
| F2 | REQUIREMENTS.md promises bug search and filtering by package, severity, status, and tag; no such page exists. | Descoped for now: requirement marked as not yet implemented. Package-page filtering (F3) covers the common case. |
| F3 | Bugs on the package page were hard-coded to open-only with no severity or status filter. | Fixed: severity and status filters as plain GET parameters. |
| F4 | ISO chooser requirement says "direct download link"; the implementation deliberately links the directory plus a filename pattern so links cannot go stale. | Fixed: requirement amended to match the recorded reasoning. |
| F5 | Decision record 0003 (two-tier freshness) has status "accepted" but tier 2 is unimplemented: nothing writes `fresh_binary_versions`, so the freshness banner was dead code. | Status corrected to "accepted, not yet implemented". Code kept: it is the read side of the planned feature. |
| F6 | "Open bugs (N)" showed at most 25 bugs with no truncation indicator and no pagination. | Fixed: truncation notice with a link to the BTS. |
| F7 | Bug page hard-coded its package link to `/p/unstable/...`, losing the reader's suite. | Fixed: uses the layout suite. |
| F8 | No suite switcher on the package page; changing suite meant editing the URL. | Fixed: suite links on the package page. |
| F9 | Autocompletion silently required two characters because the prefix was parsed as a full package name. | Fixed: a dedicated prefix parser accepts one character. |
| F10 | Autocomplete dropdown has no arrow-key navigation and no ARIA combobox semantics. | Accepted for now: needs bespoke JavaScript beyond htmx; tracked here rather than half-done. |
| F11 | Search results gave no hint when the result limit was hit. | Fixed: truncation notice. |

## Implementation quality

### What is strong

- Architecture boundaries hold: `db/etl/transform` really is the only place naming UDD columns, `internal/domain` is pure, and the consumer-owned `Queries` interface means a handler cannot see a write method. A read-only database role backs that up.
- Atomic publish: stage `__new` tables, transform, rebuild indexes once, rename-swap in one transaction. A failed sync leaves the previous data live.
- Upstream citizenship is enforced in code, not just prose: one conditional GET per run, a required identifying User-Agent with no default, `safeIdentifier` checks before every interpolated identifier.
- Templates render to a buffer first, so a half-failed render never ships a broken 200.
- Tests sit at the right layers, and CI regenerates sqlc and Tailwind output and fails on drift.
- Nix flake as the single dependency source; CI runs the same commands as local.

### Findings

| # | Finding | Status |
|---|---------|--------|
| I1 | Crash-recovery bug in the swap: `__old` tables were dropped after commit, so a crash between commit and drop made every later run fail on `ALTER TABLE ... RENAME`. | Fixed: leftover `__old` tables are dropped inside the swap transaction before renaming. |
| I2 | A 304 response with a missing cache file failed the run permanently, although nothing after a 304 needs the file: the data already lives in Postgres. With an ephemeral cache directory every run failed until someone cleared `upstream_state`. | Fixed: 304 is unchanged regardless of the cache file. |
| I3 | Dead code and false comments: the `HX-Request` fragment branch in `handleSearch` was unreachable (no template puts `hx-get` on the search form), and comments in `searchform.html` and `db/queries/packages.sql` described behaviour that did not exist. | Fixed: dead branch removed, comments corrected, and the trigram tiebreak the comment promised is now actually implemented (F9's query). |
| I4 | Source-only packages 404'd: search results link every hit to `/p/{suite}/{name}` but the package handler read only `binary_packages`. | Fixed: the handler falls back to the source package view. |
| I5 | The freshness overlay lookup checked only the alphabetically first architecture. | Fixed: prefers amd64, then all, then the first listed. |
| I6 | Popcon for source rows in `package_search` joined source names against binary popcon names, so source popularity was mostly zero. | Fixed: source popularity aggregates over the binaries built from that source. |
| I7 | `max(version)` in `package_search` sorts lexically, picking 1.9 over 1.10. | Accepted: documented in the transform; the package page is authoritative. |
| I8 | Index names accumulated a `__new` infix after every swap because renaming a table does not rename its indexes. | Fixed: the swap renames indexes to canonical names inside the transaction. |
| I9 | `layout()` costs two queries per request, including 404s, with no caching. | Accepted: fine at current scale; revisit with traffic. |
| I10 | Search ranks by popularity only, no `ts_rank`. | Accepted: deliberate, documented in the query. |
| I11 | The engineering principles mandate exponential backoff and honouring `Retry-After`, but ingest is a single-shot job with neither, and nothing said the scheduler owns retries. | Fixed: principles now state that retry policy belongs to the scheduler. |
