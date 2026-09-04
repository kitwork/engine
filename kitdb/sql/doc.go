// Package sql owns KitDB's storage-neutral relational contract.
//
// It deliberately has no dependency on the Kitwork VM or work package. SQL
// text, PostgreSQL wire clients, and Kitwork struct() declarations are all
// frontends that lower into the same catalog types and Schema IR described
// here.
package sql
