-- app_config: key/value application settings (edited via the Settings UI).
-- setting_key is the primary key; the app upserts via ON CONFLICT (setting_key).
-- (The SQL Server _Test clone table is intentionally not ported - see README.)

DROP TABLE IF EXISTS app_config CASCADE;

CREATE TABLE app_config (
    setting_key   VARCHAR(100) NOT NULL PRIMARY KEY,
    setting_value TEXT         NOT NULL,
    updated_at    TIMESTAMP    NOT NULL DEFAULT CURRENT_TIMESTAMP
);

INSERT INTO app_config (setting_key, setting_value) VALUES ('schema_version', '4');
INSERT INTO app_config (setting_key, setting_value) VALUES ('attachment_categories', 'Vendor Link,Drawing,CAD,Datasheet,Vendor Document,Fabrication,Schematic,Quote,BOM,SOP,Certificate,Photo,PDF Preview,Thumbnail');
