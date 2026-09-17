IF OBJECT_ID('dbo.app_config', 'U') IS NOT NULL DROP TABLE dbo.app_config;

CREATE TABLE app_config (
    setting_key   VARCHAR(100) NOT NULL PRIMARY KEY,
    setting_value VARCHAR(MAX) NOT NULL,
    updated_at    DATETIME     NOT NULL CONSTRAINT DF_app_config_updated_at DEFAULT GETDATE()
);

INSERT INTO app_config (setting_key, setting_value) VALUES ('schema_version', '8');
INSERT INTO app_config (setting_key, setting_value) VALUES ('attachment_categories', 'Vendor Link,Drawing,CAD,Datasheet,Vendor Document,Fabrication,Schematic,Quote,BOM,SOP,Certificate,Photo,PDF Preview,Thumbnail');
