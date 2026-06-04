// Package config provides configuration types and helpers shared across Arx apps.
package config

import (
	"fmt"
	"net/url"
	"os"
)

// Base holds configuration fields common to all Arx apps.
type Base struct {
	Version        string
	Port           string
	DBServer       string
	DBName         string
	DBUser         string
	DBPassword     string // from local.json only — never stored in .env
	SessionSecret  string
	DocControlRoot string
	TestMode       bool
	DebugMode      bool
}

// BuildDSN constructs a sqlserver:// DSN from the config fields + a password.
func (b *Base) BuildDSN(password string) string {
	u := &url.URL{
		Scheme: "sqlserver",
		User:   url.UserPassword(b.DBUser, password),
		Host:   b.DBServer,
		RawQuery: url.Values{
			"database": {b.DBName},
			"encrypt":  {"true"},
		}.Encode(),
	}
	return u.String()
}

// DSN returns a ready-to-use connection string using the stored DBPassword.
// Returns empty string if password or server is not configured.
func (b *Base) DSN() string {
	if b.DBPassword == "" || b.DBServer == "" {
		return ""
	}
	return b.BuildDSN(b.DBPassword)
}

// ConnectionSummary returns a non-sensitive description of the DB target.
func (b *Base) ConnectionSummary() string {
	if b.DBServer == "" {
		return "(not configured)"
	}
	return fmt.Sprintf("%s / %s", b.DBServer, b.DBName)
}

// Pick returns testVal when test is true, otherwise prodVal.
func Pick(test bool, testVal, prodVal string) string {
	if test {
		return testVal
	}
	return prodVal
}

// GetEnv returns the value of key, or fallback if the variable is unset or empty.
func GetEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
