package main

import "embed"

//go:embed templates
var templatesFS embed.FS

//go:embed static
var staticFS embed.FS

//go:embed RELEASE_NOTES.md
var releaseNotesData []byte
