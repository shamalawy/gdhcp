// Package web embeds the built Angular frontend into the gdhcp binary.
//
// Per amendment A4, go:embed cannot reference parent directories, so this
// file lives inside web/ itself (not server/cmd/gdhcp/main.go as
// docs/FILETREE.md's now-void snippet suggested) and embeds web/dist
// relative to this package.
package web

import (
	"embed"
	"fmt"
	"io/fs"
)

// DistFS embeds everything under web/dist, including dotfiles (the "all:"
// prefix), so `ng build`'s output — whatever files it produces — is always
// captured without this file needing to change. A committed placeholder
// web/dist/index.html keeps `go build` working before the Angular build has
// ever run.
//
//go:embed all:dist
var DistFS embed.FS

// Assets returns the filesystem to serve at "/": Angular CLI 17+ nests its
// build output under dist/<project>/browser/, while some configurations (or
// the placeholder committed here) put index.html directly under dist/.
// Assets tries dist/browser first and falls back to dist so either layout
// works without the HTTP server needing to know which one produced it.
func Assets() (fs.FS, error) {
	if sub, err := fs.Sub(DistFS, "dist/browser"); err == nil {
		if _, statErr := fs.Stat(sub, "index.html"); statErr == nil {
			return sub, nil
		}
	}

	sub, err := fs.Sub(DistFS, "dist")
	if err != nil {
		return nil, fmt.Errorf("web: sub dist: %w", err)
	}
	if _, statErr := fs.Stat(sub, "index.html"); statErr != nil {
		return nil, fmt.Errorf("web: no index.html under dist or dist/browser: %w", statErr)
	}
	return sub, nil
}
