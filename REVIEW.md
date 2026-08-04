# Project review, 2026-08-04

Features and usability first, implementation second. "Fixed" means fixed in the commit series this file arrived in.

## Verdict

Strong skeleton, unusual discipline, but the documentation ran ahead of the code: REQUIREMENTS.md and decision record 0003 promised features that did not exist. Two ETL robustness bugs and the spec gaps below are addressed as listed.

## Features and usability

What works: the ISO chooser (plain-language questions, graceful degradation for impossible combinations, verification instructions, the firmware note), real no-JS support on every page, and honest staleness everywhere the data can be behind.

| # | Finding | Status |
|---|---------|--------|
| F1 | Dependencies queried but never rendered on the package page. | Fixed: dependencies section. |
| F2 | Promised bug search page does not exist. | Descoped: requirement marked as planned. |
| F3 | Package-page bugs hard-coded to open-only, no filters. | Fixed: status and severity filters as GET parameters. |
| F4 | Requirement said "direct download link"; the chooser deliberately links directory plus pattern. | Fixed: requirement amended to match the reasoning. |
| F5 | ADR 0003 said "accepted" but tier 2 has no writer; the freshness banner was dead code. | Status corrected. Read side kept. |
| F6 | "Open bugs (N)" listed at most 25 with no truncation indicator. | Fixed: truncation notice linking the BTS. |
| F7 | Bug page hard-coded its package link to unstable. | Fixed: uses the reader's suite. |
| F8 | No suite switcher on the package page. | Fixed: suite links. |
| F9 | Autocompletion silently required two characters. | Fixed: dedicated prefix parser. |
| F10 | Autocomplete has no keyboard navigation or combobox semantics. | Accepted: needs bespoke JavaScript; tracked here. |
| F11 | Search gave no hint when hitting the result limit. | Fixed: truncation notice. |

## Implementation

What holds: transform SQL is the only place naming UDD columns, the consumer-owned query interface keeps write methods out of handlers, the rename swap publishes atomically, upstream etiquette is enforced in code, templates render to a buffer before writing, tests sit at the right layers, CI fails on generated-file drift.

| # | Finding | Status |
|---|---------|--------|
| I1 | Crash between swap commit and the deferred drop left a `__old` table that broke every later run. | Fixed: the swap drops and renames inside one transaction. |
| I2 | A 304 with a missing cache file failed the run permanently, though nothing after a 304 needs the file. | Fixed: 304 is unchanged regardless of the cache. |
| I3 | Unreachable `HX-Request` branch in the search handler; comments described htmx wiring and a trigram tiebreak that did not exist. | Fixed: branch removed, comments corrected, trigram fallback implemented. |
| I4 | Source-only packages 404'd although search links them. | Fixed: fallback to the source record. |
| I5 | Freshness lookup used the alphabetically first architecture. | Fixed: prefers amd64, then all. |
| I6 | Source popularity joined source names against binary popcon names, so it was mostly zero. | Fixed: aggregated over the source's binaries. |
| I7 | `max(version)` sorts lexically, picking 1.9 over 1.10. | Accepted: documented; the package page is authoritative. |
| I8 | Index names gained a `__new` infix on every swap. | Fixed: renamed inside the swap transaction. |
| I9 | Two layout queries per request, no caching. | Accepted: fine at this scale. |
| I10 | Search ranks by popularity only. | Accepted: deliberate. |
| I11 | Principles demanded backoff but nothing said the scheduler owns it. | Fixed: principles name the scheduler. |
