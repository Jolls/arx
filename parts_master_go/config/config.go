package config

import arxbase "arx/arxlib/config"

// AppVersion is set at build time via -ldflags from the top entry in CHANGELOG.md.
// Falls back to "dev" when running with `go run`.
var AppVersion = "dev"

// Config, PODefaults, ExpectedSchemaVersion, and all *Table() helpers are now provided by arxlib/config.
type Config = arxbase.Config
type PODefaults = arxbase.PODefaults

const ExpectedSchemaVersion = arxbase.ExpectedSchemaVersion

// Load reads configuration and injects the build-time AppVersion.
func Load() *Config {
	return arxbase.Load(AppVersion)
}
