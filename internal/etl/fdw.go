package etl

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

// uddSchema is the schema the transform queries read. Its name appears in
// db/etl/transform/*.sql and must match.
const uddSchema = "udd_raw"

// fdwServer names the foreign server pointing at the scratch database.
const fdwServer = "udd_scratch"

// exposeScratchSchema republishes the restored scratch database as the udd_raw
// schema of the application database.
//
// This exists because pg_restore cannot remap a schema. The dump names its
// tables in public, which is where our own schema lives, so the restore has to
// land in a separate database. postgres_fdw then makes those tables readable by
// ordinary SQL, which is what keeps the transform queries free of any awareness
// that their input arrived over a foreign data wrapper.
func exposeScratchSchema(
	ctx context.Context,
	pool *pgxpool.Pool,
	settings ConnectionSettings,
	scratchDB string,
	tables []string,
) error {
	for _, table := range tables {
		if !safeIdentifier.MatchString(table) {
			return fmt.Errorf("etl: table name %q is not a plain identifier", table)
		}
	}
	if !safeIdentifier.MatchString(scratchDB) {
		return fmt.Errorf("etl: scratch database name %q is not a plain identifier", scratchDB)
	}

	statements := []string{
		`CREATE EXTENSION IF NOT EXISTS postgres_fdw`,

		fmt.Sprintf(`DROP SERVER IF EXISTS %s CASCADE`, fdwServer),

		fmt.Sprintf(
			`CREATE SERVER %s FOREIGN DATA WRAPPER postgres_fdw OPTIONS (host %s, port %s, dbname %s)`,
			fdwServer,
			quoteLiteral(settings.Host),
			quoteLiteral(settings.Port),
			quoteLiteral(scratchDB),
		),

		fmt.Sprintf(
			`CREATE USER MAPPING FOR CURRENT_USER SERVER %s OPTIONS (user %s, password %s)`,
			fdwServer,
			quoteLiteral(settings.User),
			quoteLiteral(settings.Password),
		),

		fmt.Sprintf(`DROP SCHEMA IF EXISTS %s CASCADE`, uddSchema),
		fmt.Sprintf(`CREATE SCHEMA %s`, uddSchema),

		fmt.Sprintf(
			`IMPORT FOREIGN SCHEMA public LIMIT TO (%s) FROM SERVER %s INTO %s`,
			strings.Join(tables, ", "),
			fdwServer,
			uddSchema,
		),
	}

	for _, statement := range statements {
		if _, err := pool.Exec(ctx, statement); err != nil {
			return fmt.Errorf("exposing the scratch database: %w", err)
		}
	}
	return nil
}

// quoteLiteral renders a value as a Postgres string literal. DDL accepts no
// bind parameters, so option values are the one place this package builds SQL
// from data rather than from constants.
func quoteLiteral(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}
