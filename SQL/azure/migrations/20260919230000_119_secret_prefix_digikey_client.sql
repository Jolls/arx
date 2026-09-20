-- #119 — rename the DigiKey client secret's app_config key to carry the `secret_` prefix.
--
-- The Settings backup now excludes every app_config row whose key starts with `secret_`.
-- Run this together with deploying the new binary: the binary reads/writes
-- `secret_digikey_client`, so until this runs DigiKey reads an empty secret (re-enter it
-- in Settings) and the old row would still be exported.
--
-- Idempotent: renames only when the new key doesn't exist yet, drops a leftover old row
-- only once the new key exists. Does not touch app_config.schema_version.
--
-- Pinned to ArxDev. A human changes the USE line to ArxProd when running it there.
-- Single batch, no GO — the Azure portal query editor sends the whole script as one batch.

USE ArxDev;

UPDATE dbo.app_config
SET setting_key = 'secret_digikey_client'
WHERE setting_key = 'digikey_client_secret'
  AND NOT EXISTS (SELECT 1 FROM dbo.app_config WHERE setting_key = 'secret_digikey_client');

DELETE FROM dbo.app_config
WHERE setting_key = 'digikey_client_secret'
  AND EXISTS (SELECT 1 FROM dbo.app_config WHERE setting_key = 'secret_digikey_client');

IF NOT EXISTS (SELECT 1 FROM dbo.schema_migrations WHERE version_id = 20260919230000)
    INSERT INTO dbo.schema_migrations (version_id, is_applied) VALUES (20260919230000, 1);
