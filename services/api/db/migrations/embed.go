package migrations

import "embed"

// Files contains the versioned PostgreSQL migrations shipped with the API binary.
//
//go:embed *.sql
var Files embed.FS
