// Package ui embeds the built web UI so the binary is self-contained.
//
// The SvelteKit app in ../../ui compiles straight into build/ (see
// ../../ui/vite.config.ts), and the all: prefix is required because the
// bundle lives under _app/, which plain embed patterns skip. On a clean
// clone build/ only holds the tracked placeholder.html, so the pattern
// always matches and the binary still compiles without Node: the server
// serves that placeholder until `make ui-build` runs.
package ui

import "embed"

// Build is the built UI bundle, rooted at build/ (the server mounts it
// with fs.Sub(ui.Build, "build")).
//
//go:embed all:build
var Build embed.FS
