// Package web embeds the built SPA assets.
package web

import "embed"

// Dist is the Vite production build output embedded into the Go binary. WEB-1
// replaces the placeholder dist/index.html with the real SPA build.
//
//go:embed all:dist
var Dist embed.FS
