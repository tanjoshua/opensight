package api

import (
	"fmt"
	"io/fs"
	"net/http"
	"path"
	"strings"

	"opensight/web"
)

func newSPAHandler() http.Handler {
	dist, err := fs.Sub(web.Dist, "dist")
	if err != nil {
		panic(fmt.Sprintf("load embedded web dist: %v", err))
	}
	files := http.FileServer(http.FS(dist))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			writeProblem(w, http.StatusMethodNotAllowed, "method not allowed", "method not allowed")
			return
		}

		cleanPath := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
		if cleanPath == "" || cleanPath == "." {
			cleanPath = "index.html"
		}
		if f, err := dist.Open(cleanPath); err == nil {
			_ = f.Close()
			files.ServeHTTP(w, r)
			return
		}
		if path.Ext(cleanPath) != "" {
			http.NotFound(w, r)
			return
		}

		fallback := r.Clone(r.Context())
		fallback.URL.Path = "/"
		files.ServeHTTP(w, fallback)
	})
}
