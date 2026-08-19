// Package javascript is Kitwork's flattened KitJS consumer and deterministic
// delivery adapter. It scans authored HTML, closes only packages present in the
// pinned release catalog, builds one immutable Artifact, and stores its exact
// bytes under their SHA-256 identity. The standalone package repository remains
// the public source and release authority for the mirrored browser fragments.
package javascript
