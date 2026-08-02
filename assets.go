// Package sourceslist embeds the SQL the binaries execute.
//
// The files live at db/ rather than beside the Go that runs them so that they
// stay readable as SQL: sqlc reads db/queries, psql can run db/migrations by
// hand against a scratch database, and the transform queries can be pasted into
// a session while debugging a sync. Embedding them here means a deployed binary
// still carries its own schema and needs nothing mounted next to it.
package sourceslist

import "embed"

// SQL holds db/, rooted at the repository root: db/migrations, db/etl,
// db/queries, and db/seed.
//
//go:embed db
var SQL embed.FS

// Web holds web/, the templates and static assets the server renders from.
// Embedding them means a deployed binary is the whole site and needs nothing
// mounted beside it.
//
// The stylesheet under web/assets/generated is produced by Tailwind at build
// time and committed, for the same reason the sqlc output is: embedding cannot
// reach a file that does not exist, and CI checks that regenerating produces no
// diff.
//
//go:embed web/templates web/assets
var Web embed.FS
