package config

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestCheckSchemaVersion(t *testing.T) {
	get := func(val string, err error) func(context.Context, string) (string, error) {
		return func(_ context.Context, key string) (string, error) {
			if key != "schema_version" {
				t.Errorf("read key %q, want schema_version", key)
			}
			return val, err
		}
	}

	mismatch, connErr := CheckSchemaVersion(context.Background(), get(ExpectedSchemaVersion, nil))
	if mismatch != "" || connErr != "" {
		t.Errorf("matching version: mismatch=%q connErr=%q", mismatch, connErr)
	}

	mismatch, connErr = CheckSchemaVersion(context.Background(), get("0", nil))
	if connErr != "" || !strings.HasPrefix(mismatch, "DB schema v0, app expects v") {
		t.Errorf("old version: mismatch=%q connErr=%q", mismatch, connErr)
	}

	mismatch, connErr = CheckSchemaVersion(context.Background(), get("", errors.New("boom")))
	if mismatch != "" || connErr != "could not read schema_version (boom)" {
		t.Errorf("read error: mismatch=%q connErr=%q", mismatch, connErr)
	}
}
