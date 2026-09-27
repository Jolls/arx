// Package config provides configuration types and helpers for Arx.
package config

import (
	"fmt"
	"net/url"
	"os"
)

// SessionCookieName is the single gorilla/sessions cookie name, so the
// single-origin app carries one session + CSRF token.
const SessionCookieName = "arx-session"

// Base holds configuration fields common to all Arx configuration.
//
// The Test* connection fields form a second connection profile used while test
// mode is active. Each Test* field overrides its prod counterpart only when
// non-empty; a blank field inherits the prod value. This lets test mode point
// at an entirely separate server/credentials while keeping the historical
// same-server, name-only swap working when only TestDBName is set.
type Base struct {
	Version        string
	Port           string
	DBServer       string
	DBName         string
	DBUser         string
	DBPassword     string // from local.json only — never stored in .env
	TestDBServer   string // test-mode override; inherits DBServer when blank
	TestDBName     string // database to use in test mode (default "ArxDev")
	TestDBUser     string // test-mode override; inherits DBUser when blank
	TestDBPassword string // from local.json only; inherits DBPassword when blank
	SessionSecret  string
	DocControlRoot string
	TestMode       bool
	DebugMode      bool
}

// ActiveDBName returns the database the app should connect to: the dev DB in
// test mode, otherwise the prod DB.
func (b *Base) ActiveDBName() string { return Pick(b.TestMode, b.TestDBName, b.DBName) }

// testOverride returns testVal when test mode is active and testVal is
// non-empty, otherwise prodVal. It centralizes the "a non-empty Test* field
// overrides its prod counterpart" rule shared by the active-profile resolvers.
func (b *Base) testOverride(testVal, prodVal string) string {
	if b.TestMode && testVal != "" {
		return testVal
	}
	return prodVal
}

// activeServer, activeUser, and activePassword resolve each connection field
// for the currently-selected profile.
func (b *Base) activeServer() string   { return b.testOverride(b.TestDBServer, b.DBServer) }
func (b *Base) activeUser() string     { return b.testOverride(b.TestDBUser, b.DBUser) }
func (b *Base) activePassword() string { return b.testOverride(b.TestDBPassword, b.DBPassword) }

// BuildDSN constructs a postgres:// connection string for the active profile
// from the config fields + a password. sslmode=require forces TLS and refuses a
// plaintext fallback (#757). (require encrypts but does not verify the server
// cert; #666 puts the DB on a separate box, so plaintext must never be silently
// used.)
func (b *Base) BuildDSN(password string) string {
	u := &url.URL{
		Scheme:   "postgres",
		User:     url.UserPassword(b.activeUser(), password),
		Host:     b.activeServer(),
		Path:     "/" + b.ActiveDBName(),
		RawQuery: url.Values{"sslmode": {"require"}}.Encode(),
	}
	return u.String()
}

// DSN returns a ready-to-use connection string using the active profile's
// stored password. Returns empty string if password or server is not configured.
func (b *Base) DSN() string {
	if b.activePassword() == "" || b.activeServer() == "" {
		return ""
	}
	return b.BuildDSN(b.activePassword())
}

// ConnectionSummary returns a non-sensitive description of the active DB target.
func (b *Base) ConnectionSummary() string {
	if b.activeServer() == "" {
		return "(not configured)"
	}
	name := b.ActiveDBName()
	if b.TestMode {
		name += " (TEST)"
	}
	return fmt.Sprintf("%s / %s", b.activeServer(), name)
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
