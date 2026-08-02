# 0001. Source bulk data from UDD

Status: accepted

## Context

`sourcesli.st` browses Debian packages and bug reports. That data has to come from somewhere, and the obvious paths are hostile to the people who run Debian's infrastructure.

Parsing the archive directly means fetching `Packages` and `Sources` indices per suite, component, and architecture, then tracking `InRelease` to know when they changed. This is well-trodden and `apt` does it politely, but it only covers package metadata. It says nothing about bugs.

Bugs are the problem. The BTS exposes a SOAP interface intended for per-bug lookups. Using it to populate a browsable index means thousands of small unbatched requests against a service that was never designed for bulk reads, which is exactly the behaviour that gets a client blocked. Scraping `bugs.debian.org` HTML is worse.

The [Ultimate Debian Database](https://wiki.debian.org/UltimateDebianDatabase) already solves this. Debian QA aggregates `Packages` and `Sources`, BTS bugs, popcon, upload history, migrations, lintian results, and more into one Postgres database, and publishes it for exactly this kind of consumer.

## Decision

All bulk Debian data comes from UDD, and specifically from the dump at `https://udd.debian.org/dumps/udd.dump`: 2.4 GiB as of August 2026 and growing, regenerated every two days. It is the only upstream source an automated job may touch.

UDD also publishes a read-only mirror at `postgresql://udd-mirror:udd-mirror@udd-mirror.debian.net:5432/udd`. We use it only for interactive exploration by a human, never for scheduled ingest.

The dump wins because its cost to Debian is fixed and inert. It is a static file, so the bandwidth is the same whatever we do, and a slow query we wrote badly cannot become somebody else's incident. A query against the shared mirror spends CPU, connections, and I/O that other consumers are waiting on, and the mirror is documented as a convenience for ad-hoc querying rather than as an ETL source. The failure mode of the dump is that we waste our own time; the failure mode of the mirror is that we degrade a service other people depend on.

The sync therefore runs as:

1. Conditional `GET` of the dump, sending the `ETag` and `Last-Modified` recorded by the previous run. A `304` ends the run having transferred no body.
2. Selective `pg_restore --table` into a scratch database, covering only the tables we map.
3. Transformation in SQL from the scratch database into our own staging tables, then an atomic swap.
4. Drop of the scratch database.

UDD's schema is treated as an input, not a contract. Step 3 is the only place that knows UDD's column names, so an upstream rename breaks one adapter rather than the application.

Sync cadence follows publication cadence: no more often than every two days. A user request never triggers an upstream request.

We do not fetch archive indices ourselves unless UDD stops carrying something we need. If that happens, it comes from `deb.debian.org` using conditional requests and pdiffs, and gets its own decision record.

ISO metadata is the one gap: UDD does not carry it. It is derived from the published checksum files under `cdimage.debian.org`, fetched conditionally on a schedule measured in days, and cached like everything else.

## Consequences

Upstream load becomes constant and decoupled from our traffic. Ten users or ten million, Debian sees one sync every two days. This is the property that makes the project defensible to run at all.

We inherit UDD's freshness rather than setting our own. Data can be up to a few days behind the archive. Staleness is shown to the user rather than hidden, and every page links back to the authoritative Debian page.

We inherit UDD's coverage. Anything UDD does not import is not available to us without a new upstream integration, which is a deliberate speed bump.

Our ingest becomes mostly SQL: read from one Postgres, write to another. That is a much smaller and more testable job than a set of format parsers, and it removes the need to implement `InRelease` verification, pdiff application, and BTS pagination.

We pay for the choice in local resources rather than in upstream load. A sync downloads the whole dump, 2.4 GiB as of August 2026, and needs scratch disk and a Postgres instance to restore into. Selective restore keeps that bounded, and the cost falls entirely on infrastructure we pay for.

We cannot sync more often than the dump is published, even if we wanted to. That is a feature: it makes the polite cadence the only cadence, rather than a discipline someone has to remember.

Development, tests, and CI never contact Debian at all. Fixtures are checked in, so a broken test loop cannot turn into upstream load.
