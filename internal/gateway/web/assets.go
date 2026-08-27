package web

import "embed"

//go:embed index.html app.js
var staticFiles embed.FS
