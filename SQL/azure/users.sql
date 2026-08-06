IF OBJECT_ID('dbo.users', 'U') IS NOT NULL DROP TABLE dbo.users;

CREATE TABLE dbo.users (
    id            INT           PRIMARY KEY IDENTITY,
    username      VARCHAR(64)   NOT NULL,
    display_name  VARCHAR(128)  NOT NULL,
    password_hash VARCHAR(255)  NOT NULL,
    is_active     BIT           NOT NULL DEFAULT 1,
    can_approve_po BIT          NOT NULL DEFAULT 0,  -- may approve/reject POs (issue #267)
    can_approve_records BIT     NOT NULL DEFAULT 0,  -- may approve/unlock approved test records (issue #249)
    is_admin      BIT           NOT NULL DEFAULT 0,  -- may manage user accounts (create/reset/deactivate/grant) (issue #750)
    default_po_contact_id  INT   NULL,               -- per-user default PO receiver contact (issue #463)
    default_po_receiver_id INT   NULL,               -- per-user default PO receiver company (issue #463)
    accent_color  VARCHAR(20)   NULL,                -- per-user UI accent theme key, e.g. 'teal' (issue #537)
    default_route VARCHAR(255)  NULL,                -- per-user landing page after login: a same-origin relative path ('/', '/pos', or a custom filtered route like '/?f0=as'); NULL = '/' (issue #282)
    timezone      VARCHAR(64)   NOT NULL DEFAULT 'America/Los_Angeles',  -- per-user IANA timezone for calendar-day bucketing of UTC audit timestamps (issue #847)
    created_at    DATETIME      NOT NULL DEFAULT GETDATE(),
    updated_at    DATETIME      NOT NULL DEFAULT GETDATE(),
    CONSTRAINT UQ_users_username UNIQUE (username)
);
