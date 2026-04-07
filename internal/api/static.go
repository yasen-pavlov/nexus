package api

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed all:static
var staticFiles embed.FS

func staticFileHandler() http.Handler {
	subFS, err := fs.Sub(staticFiles, "static")
	if err != nil {
		return nil
	}

	// Check if the static directory has any files (it won't during dev without a build)
	entries, err := fs.ReadDir(subFS, ".")
	if err != nil || len(entries) == 0 {
		return nil
	}

	fileServer := http.FileServer(http.FS(subFS))

	// SPA fallback: serve index.html for any path that doesn't match a static file
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Try to serve the file directly
		path := r.URL.Path
		if path == "/" {
			path = "index.html"
		}

		if _, err := fs.Stat(subFS, path[1:]); err != nil {
			// File not found, serve index.html for SPA routing
			r.URL.Path = "/"
		}
		fileServer.ServeHTTP(w, r)
	})
}
