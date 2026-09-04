// Package node owns a bounded fleet of embedded KitDB handles.
//
// A Manager shares one handle for each canonical path, pins it with explicit
// leases while callers are using it, and keeps released handles in an idle LRU.
// Admission evicts the coldest idle handle when either the open-handle or
// reserved page-cache budget is full. If every candidate is leased, Acquire
// applies context-cancelable backpressure instead of exceeding the limits.
//
// The same owner schedules a bounded set of typed maintenance operations. Jobs
// with the same exact operation identity coalesce, only one touches a canonical
// path at a time, and weighted priority prevents background work from starving.
// Database resources borrow regular leases; backup destinations and replica
// mailboxes are reservation-only and never consume a handle or page-cache
// budget. Maintenance never participates in transaction or WAL durability.
//
// A host may also register local filesystem replica links. One shared
// dispatcher and a fixed worker pool compose the existing publish, apply, and
// acknowledge jobs with bounded retry/backoff. Link configuration and health
// are process-local; durable progress remains only in the source pin, target
// WAL cursor, and mailbox artifacts, so manager restart needs no cursor file.
//
// A host may register production protection policies on the same Manager.
// Policies share one bounded dispatcher/worker pool, compose verified backup
// maintenance, perform independent logical-digest restore drills, prune only
// fully verified same-identity anchors, and derive ready/degraded/unsafe health
// from explicit RPO/RTO ages. Policy configuration is process-local; restart
// truth is reconstructed from immutable anchors and the durable source pin.
//
// The package does not redefine schema or replication semantics and does not
// create a second persistence path. Those remain responsibilities of kitdb;
// node only owns bounded admission, handles, and operation lifecycle.
package node
