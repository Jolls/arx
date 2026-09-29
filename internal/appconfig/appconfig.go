// Package appconfig reads and writes the app_config key/value table (#190, #248) via the
// sqlc-generated queries in appconfig.sql. It is shared infrastructure (company logo, DigiKey
// credentials, the schema-version gate, Settings), not a business domain.
package appconfig

import (
	"context"

	"arx/internal/dbq"
)

type Service struct{ q *dbq.Queries }

func New(db dbq.DBTX) *Service { return &Service{q: dbq.New(db)} }

// Get returns the value for key; a missing key yields sql.ErrNoRows.
func (s *Service) Get(ctx context.Context, key string) (string, error) {
	return s.q.GetAppSetting(ctx, key)
}

// Set upserts key, bumping updated_at when it already exists.
func (s *Service) Set(ctx context.Context, key, value string) error {
	return s.q.UpsertAppSetting(ctx, dbq.UpsertAppSettingParams{SettingKey: key, SettingValue: value})
}
