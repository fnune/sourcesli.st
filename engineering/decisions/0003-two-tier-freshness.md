# 0003. Two-tier freshness

Status: accepted, tier 2 not yet implemented

Tier 1 is live. Of tier 2 only the read side exists: the overlay table, the web tier reading it first, and the truncation on every bulk load. Until the writer is built, package versions stay at dump freshness and the site says so.

Builds on [0001](0001-source-bulk-data-from-udd.md).

## Context

The UDD dump is regenerated every two days, so a site built only on it is up to two days behind. For most of what we show that is fine. For one thing it is not: a new version landing in unstable is the change readers notice, and being two days late on it undermines the point of a package browser.

The obvious fixes are the ones 0001 rejected. Per-bug BTS calls and bulk queries against the shared UDD mirror both spend other people's resources to save us latency.

There is a third source that carries no such objection. The archive indices on `deb.debian.org` exist to be polled: every `apt update` on every Debian machine does it. Debian publishes pdiffs (`Packages.diff/Index`) so clients transfer deltas rather than whole indices, and `deb.debian.org` is a CDN sized for millions of clients. One consumer polling with conditional requests is indistinguishable from a rounding error.

## Decision

Freshness comes in two tiers.

Tier 1, bulk, every two days: the UDD dump, exactly as described in 0001. It is the source of truth for everything.

Tier 2, delta, hourly: binary and source package versions for the fast-moving suites, from `deb.debian.org`.

Tier 2 is implemented by running `apt-get update` inside a container with our own `sources.list`, then reading `/var/lib/apt/lists/*_Packages`. We do not implement `InRelease` signature verification, pdiff application, or conditional-request handling ourselves. apt already does all of it correctly, and reimplementing OpenPGP verification to avoid a container would be a bad trade in both effort and risk.

Tier 2 writes to an overlay table, `fresh_binary_versions`, rather than into `binary_packages`. The web tier reads the overlay with priority and falls back to the bulk tables. The tier 1 load truncates the overlay as part of its atomic swap.

The overlay exists because the two tiers have incompatible write patterns: tier 1 replaces whole tables by renaming them into place, and an incremental writer cannot participate in that swap. Keeping deltas in their own table means no merge logic, no lock contention between the tiers, and a site that degrades to dump freshness rather than breaking when tier 2 fails.

Bug data stays at tier 1 freshness. Every bug view states the date the data is from and links to the BTS for live state.

## Consequences

Package versions are at most an hour behind the archive. Bugs are up to two days behind, and say so.

We take on a second upstream integration and a second failure mode. It is contained: tier 2 failing means stale versions, not an outage, and the overlay is disposable.

We depend on apt's behaviour rather than on a parser we own. That is deliberate. The parsing surface we keep is `Packages` stanzas from a local file, which is stable and easy to test from fixtures.

The rule stated in 0001 as "no automated job queries `udd-mirror.debian.net`" is superseded by a rule about cost shape rather than hostname: an automated job may not place unbounded or bulk load on shared Debian infrastructure. The mirror still fails that test for our workload, so nothing about our use of it changes.
