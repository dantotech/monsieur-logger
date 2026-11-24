// Package web holds static web assets (the Swagger UI page and the logo).
package web

import "embed"

// Swagger contains swagger/index.html and swagger/logo.svg.
//
//go:embed swagger
var Swagger embed.FS
