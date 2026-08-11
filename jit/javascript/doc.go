// Package javascript provides KitJS's Go-owned module registry, executable HTML
// scanner, dependency resolver, and deterministic classic-script composer.
//
// Browser source files remain independently executable JavaScript. Kitwork
// embeds them, resolves the exact component graph and optional Drive core for a
// generation, and emits one content-addressed kit.js without Node, npm, or a
// browser-side loader.
package javascript
