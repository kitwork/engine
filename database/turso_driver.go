package database

import _ "turso.tech/database/tursogo" // registers the "turso" database/sql driver (no cgo; purego + native blob)

// TursoBuildTag indicates the Turso driver is registered and available.
const TursoBuildTag = true
