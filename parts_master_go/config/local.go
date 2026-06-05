package config

import arxbase "arx/arxlib/config"

// LocalConfig, LoadLocal, and SaveLocal are now provided by arxlib/config.
// Both apps share a single config/local.json so settings are consistent.
type LocalConfig = arxbase.LocalConfig

var LoadLocal = arxbase.LoadLocal
var SaveLocal = arxbase.SaveLocal
