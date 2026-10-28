package db

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/url"
	"sort"
	"strings"
	"syscall"

	"github.com/jackc/pgx/v5/pgconn"
)

// ConnectErrorInfo is a user-facing classification of a failed connect.
// Code is a stable identifier documented in docs/db-connection-errors.md;
// Details is the raw error with secrets masked.
type ConnectErrorInfo struct {
	Code, Message, Details string
}

// ConnectErrorCodes lists every code ClassifyConnectError can return; each has
// a section in docs/db-connection-errors.md.
var ConnectErrorCodes = []string{
	"db_auth_failed",
	"db_role_unknown",
	"db_database_unknown",
	"db_firewall_blocked",
	"db_host_unreachable",
	"db_tls_failed",
	"db_connect_failed",
}

// ClassifyConnectError maps a Connect error to a friendly message and code.
// user is the DB login (shown in some messages); secrets (password, DSN) are
// masked out of Details.
func ClassifyConnectError(err error, user string, secrets ...string) ConnectErrorInfo {
	code, msg := classifyConnectError(err, user)
	return ConnectErrorInfo{Code: code, Message: msg, Details: redact(err.Error(), secrets)}
}

func classifyConnectError(err error, user string) (code, msg string) {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch {
		case strings.Contains(strings.ToLower(pgErr.Message), "pg_hba.conf"):
			return "db_firewall_blocked", "The server refused this computer's address. Ask the database administrator to allow your IP address in the server's firewall or access rules."
		case pgErr.Code == "28P01":
			return "db_auth_failed", fmt.Sprintf("The server rejected the password for user %s. Re-enter it below, or check whether it was changed on the server.", user)
		case pgErr.Code == "28000":
			return "db_role_unknown", fmt.Sprintf("The server does not accept user %s. Check the user name below, or ask the database administrator whether the account exists and may log in.", user)
		case pgErr.Code == "3D000":
			return "db_database_unknown", "The server has no database with that name. Check the database name below."
		}
	}

	var (
		unknownAuth x509.UnknownAuthorityError
		hostname    x509.HostnameError
		certInvalid x509.CertificateInvalidError
		recordHdr   tls.RecordHeaderError
		certVerify  *tls.CertificateVerificationError
	)
	if errors.As(err, &unknownAuth) || errors.As(err, &hostname) || errors.As(err, &certInvalid) ||
		errors.As(err, &recordHdr) || errors.As(err, &certVerify) ||
		strings.Contains(err.Error(), "server refused TLS connection") || strings.Contains(err.Error(), "tls error") {
		return "db_tls_failed", "The secure (TLS) connection to the server failed. Check that the server address and port are correct and that the server has TLS enabled."
	}

	var dnsErr *net.DNSError
	var netErr net.Error
	var opErr *net.OpError
	if errors.As(err, &dnsErr) || (errors.As(err, &netErr) && netErr.Timeout()) ||
		errors.As(err, &opErr) || errors.Is(err, syscall.ECONNREFUSED) {
		return "db_host_unreachable", "Could not reach the database server. Check the server address and port, your network or VPN, and that the server is running."
	}

	return "db_connect_failed", "Could not connect to the database"
}

// redact masks every non-empty secret (and its URL-escaped forms) in s.
func redact(s string, secrets []string) string {
	var forms []string
	for _, sec := range secrets {
		if sec == "" {
			continue
		}
		forms = append(forms, sec, url.QueryEscape(sec), url.PathEscape(sec))
	}
	// Longest first so a DSN is masked whole before its embedded password.
	sort.Slice(forms, func(i, j int) bool { return len(forms[i]) > len(forms[j]) })
	for _, f := range forms {
		s = strings.ReplaceAll(s, f, "***")
	}
	return s
}
