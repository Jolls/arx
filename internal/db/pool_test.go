package db

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/stdlib"
)

func TestOpenPool_Limits(t *testing.T) {
	database, err := openPool("postgres://u:p@127.0.0.1:1/x?sslmode=require") // never dialed
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if got := database.Stats().MaxOpenConnections; got != 12 {
		t.Errorf("MaxOpenConnections = %d, want 12", got)
	}
}

func TestShouldPing_Threshold(t *testing.T) {
	for _, c := range []struct {
		idle time.Duration
		want bool
	}{{29 * time.Second, false}, {30 * time.Second, false}, {31 * time.Second, true}} {
		if got := shouldPing(context.Background(), stdlib.ShouldPingParams{IdleDuration: c.idle}); got != c.want {
			t.Errorf("shouldPing(idle %s) = %v, want %v", c.idle, got, c.want)
		}
	}
}

func TestConnect_Errors(t *testing.T) {
	_, err := Connect("postgres://u:SECRETPW@127.0.0.1:1/x?sslmode=require")
	if err == nil {
		t.Fatal("unreachable server: want error")
	}
	if !strings.HasPrefix(err.Error(), "ping: ") {
		t.Errorf("unreachable server: error %q lacks ping prefix", err)
	}
	if strings.Contains(err.Error(), "SECRETPW") {
		t.Error("unreachable server: error leaks the password")
	}

	_, err = Connect("postgres://u:SECRETPW@h:badport/x")
	if err == nil {
		t.Fatal("bad dsn: want error")
	}
	if strings.Contains(err.Error(), "SECRETPW") {
		t.Error("bad dsn: error leaks the password")
	}
}
