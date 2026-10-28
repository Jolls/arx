package db

import (
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

type timeoutErr struct{}

func (timeoutErr) Error() string   { return "i/o timeout" }
func (timeoutErr) Timeout() bool   { return true }
func (timeoutErr) Temporary() bool { return true }

func TestClassifyConnectError(t *testing.T) {
	const fallback = "Could not connect to the database"
	tests := []struct {
		name     string
		err      error
		wantCode string
		wantMsg  string // substring
	}{
		{"bad password", &pgconn.PgError{Code: "28P01", Message: "password authentication failed"}, "db_auth_failed", "arx_app"},
		{"firewall", &pgconn.PgError{Code: "28000", Message: "no pg_hba.conf entry for host"}, "db_firewall_blocked", ""},
		{"unknown role", &pgconn.PgError{Code: "28000", Message: "role does not exist"}, "db_role_unknown", "arx_app"},
		{"unknown database", &pgconn.PgError{Code: "3D000", Message: "database does not exist"}, "db_database_unknown", ""},
		{"dns", &net.DNSError{Err: "no such host", Name: "db.example"}, "db_host_unreachable", ""},
		{"timeout", timeoutErr{}, "db_host_unreachable", ""},
		{"conn refused", &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")}, "db_host_unreachable", ""},
		{"x509", x509.UnknownAuthorityError{}, "db_tls_failed", ""},
		{"tls refused", errors.New("server refused TLS connection"), "db_tls_failed", ""},
		{"unclassified text", errors.New("something odd"), "db_connect_failed", fallback},
		{"other pg error", &pgconn.PgError{Code: "53300", Message: "too many connections"}, "db_connect_failed", fallback},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ClassifyConnectError(fmt.Errorf("ping: %w", tc.err), "arx_app")
			if got.Code != tc.wantCode {
				t.Errorf("Code = %q, want %q", got.Code, tc.wantCode)
			}
			if got.Message == "" || !strings.Contains(got.Message, tc.wantMsg) {
				t.Errorf("Message = %q, want non-empty containing %q", got.Message, tc.wantMsg)
			}
			if got.Details == "" {
				t.Error("Details empty, want raw error text")
			}
		})
	}
}

func TestClassifyConnectError_DetailsRedactsSecrets(t *testing.T) {
	const pw = "s3cretPW"
	const dsn = "postgres://arx_app:" + pw + "@host/db"
	err := fmt.Errorf("ping: failed to connect using %s (password %s)", dsn, pw)
	got := ClassifyConnectError(err, "arx_app", pw, dsn)
	if got.Details == "" || !strings.Contains(got.Details, "***") {
		t.Errorf("Details = %q, want non-empty with ***", got.Details)
	}
	for _, s := range []string{pw, dsn} {
		if strings.Contains(got.Details, s) || strings.Contains(got.Message, s) {
			t.Errorf("secret %q leaked: details=%q message=%q", s, got.Details, got.Message)
		}
	}
}

func TestDBConnectionErrorsDocHasSectionPerCode(t *testing.T) {
	b, err := os.ReadFile("../../docs/db-connection-errors.md")
	if err != nil {
		t.Fatal(err)
	}
	if len(ConnectErrorCodes) != 7 {
		t.Fatalf("ConnectErrorCodes = %v, want 7 codes", ConnectErrorCodes)
	}
	for _, code := range ConnectErrorCodes {
		if !strings.Contains(string(b), "\n## "+code+"\n") {
			t.Errorf("docs/db-connection-errors.md missing '## %s' section", code)
		}
	}
}
