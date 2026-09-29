-- app_config key/value settings (#190, #248). sqlc generates internal/dbq/appconfig.sql.go from
-- this file; the service is appconfig.go. Infrastructure shared by every handler (company logo,
-- DigiKey credentials, schema-version gate, Settings), so it is not tied to one domain.

-- name: GetAppSetting :one
SELECT setting_value FROM app_config WHERE setting_key = sqlc.arg(setting_key);

-- name: UpsertAppSetting :exec
INSERT INTO app_config (setting_key, setting_value) VALUES (sqlc.arg(setting_key), sqlc.arg(setting_value))
ON CONFLICT (setting_key) DO UPDATE SET setting_value = EXCLUDED.setting_value, updated_at = CURRENT_TIMESTAMP;
