package db

import "embed"

// migrationsFS embeds every migration file at build time so the single
// gdhcp binary needs no filesystem access to migrate itself (amendment A4).
// Naming: NNNN_description.sql, executed in ascending numeric order; gaps in
// numbering are expected (e.g. 0002 is owned by a peer unit) and tolerated
// by the Migrator.
//
//go:embed migrations/*.sql
var migrationsFS embed.FS
