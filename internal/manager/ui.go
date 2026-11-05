package manager

import (
	"embed"
	"net/http"
	"path"
	"strings"
)

//go:embed web/*
var webAssets embed.FS

var indexHTML = func() string {
	data, err := webAssets.ReadFile("web/index.html")
	if err != nil {
		panic(err)
	}
	return string(data)
}()

func (s *Server) asset(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/assets/")
	if name != path.Base(name) || (path.Ext(name) != ".js" && path.Ext(name) != ".css") {
		http.NotFound(w, r)
		return
	}
	data, err := webAssets.ReadFile("web/" + name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if path.Ext(name) == ".js" {
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	} else {
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
	}
	_, _ = w.Write(data)
}
