// Package dashboard serves a small static HTML/JS dashboard from the
// embedded filesystem. The dashboard talks to the REST API on the
// same origin.
package dashboard

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed static/*
var assets embed.FS

// Handler returns an http.Handler that serves the dashboard.
func Handler() http.Handler {
	sub, err := fs.Sub(assets, "static")
	if err != nil {
		return http.NotFoundHandler()
	}
	return http.FileServer(http.FS(sub))
}
