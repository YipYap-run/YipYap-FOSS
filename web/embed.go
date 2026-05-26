package web

import "embed"

// Dist embeds the built web assets from the dist directory.
//
//go:embed dist/*
var Dist embed.FS
