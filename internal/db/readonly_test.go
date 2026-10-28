package db

import (
	"strings"
	"net/url"
	"testing"
)

func TestReadOnlyDSN(t *testing.T) {
	// A pre-existing read-write override must not survive.
	got, err := ReadOnlyDSN("postgres://u:p@h:5432/ArxDev?sslmode=require&default_transaction_read_only=off")
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(got)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	if q.Get("default_transaction_read_only") != "on" {
		t.Errorf("default_transaction_read_only = %q, want on", q.Get("default_transaction_read_only"))
	}
	if q.Get("statement_timeout") != "30000" {
		t.Errorf("statement_timeout = %q, want 30000", q.Get("statement_timeout"))
	}
	if q.Get("sslmode") != "require" || u.Host != "h:5432" || u.Path != "/ArxDev" {
		t.Errorf("rest of DSN changed: %s", got)
	}
}

func TestOpenReadOnly_DoesNotDial(t *testing.T) {
	// Port 1 is unreachable; a lazy open must still succeed.
	d, err := OpenReadOnly("postgres://u@127.0.0.1:1/x?sslmode=require")
	if err != nil {
		t.Fatalf("OpenReadOnly: %v", err)
	}
	d.Close()
}

func TestReadOnlyDSN_ErrorOmitsPassword(t *testing.T) {
	_, err := ReadOnlyDSN("postgres://u:SECRETPW@h:badport/x")
	if err == nil {
		t.Fatal("want parse error")
	}
	if strings.Contains(err.Error(), "SECRETPW") {
		t.Errorf("error leaks password: %v", err)
	}
}
