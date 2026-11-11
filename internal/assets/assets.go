package assets

import "embed"

//go:embed site/*
var Site embed.FS

//go:embed admin/*
var Admin embed.FS

//go:embed readme.md.tmpl
var ReadmeTemplate string
