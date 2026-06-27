IF OBJECT_ID('dbo.users', 'U') IS NOT NULL DROP TABLE dbo.users;

CREATE TABLE dbo.users (
    id            INT           PRIMARY KEY IDENTITY,
    username      VARCHAR(64)   NOT NULL,
    display_name  VARCHAR(128)  NOT NULL,
    password_hash VARCHAR(255)  NOT NULL,
    is_active     BIT           NOT NULL DEFAULT 1,
    can_approve_po BIT          NOT NULL DEFAULT 0,  -- may approve/reject POs (issue #267)
    can_approve_records BIT     NOT NULL DEFAULT 0,  -- may approve/unlock approved test records (issue #249)
    created_at    DATETIME      NOT NULL DEFAULT GETDATE(),
    updated_at    DATETIME      NOT NULL DEFAULT GETDATE(),
    CONSTRAINT UQ_users_username UNIQUE (username)
);
