// Package version is the engine's release number: one constant that every part reads. It lives in a
// package of its own, importing nothing, so the packages the root package imports (render, work, ui)
// can read it without an import cycle.
package version

// Number is the engine release: what `kitwork version` prints and what every page names in its
// <meta name="generator" content="Kitwork …">.
const Number = "0.9.0"
