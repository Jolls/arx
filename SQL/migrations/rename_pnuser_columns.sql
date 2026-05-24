-- rename_pnuser_columns.sql
-- Renames PNUser1-10 to user_field_1-10 on both PN and PN_Test.
-- sp_rename on columns is safe to run on a live DB — no data movement.
-- Run against the PartsMaster database.

-- PN (production)
EXEC sp_rename 'dbo.PN.PNUser1',  'user_field_1',  'COLUMN';
EXEC sp_rename 'dbo.PN.PNUser2',  'user_field_2',  'COLUMN';
EXEC sp_rename 'dbo.PN.PNUser3',  'user_field_3',  'COLUMN';
EXEC sp_rename 'dbo.PN.PNUser4',  'user_field_4',  'COLUMN';
EXEC sp_rename 'dbo.PN.PNUser5',  'user_field_5',  'COLUMN';
EXEC sp_rename 'dbo.PN.PNUser6',  'user_field_6',  'COLUMN';
EXEC sp_rename 'dbo.PN.PNUser7',  'user_field_7',  'COLUMN';
EXEC sp_rename 'dbo.PN.PNUser8',  'user_field_8',  'COLUMN';
EXEC sp_rename 'dbo.PN.PNUser9',  'user_field_9',  'COLUMN';
EXEC sp_rename 'dbo.PN.PNUser10', 'user_field_10', 'COLUMN';

-- PN_Test (test mode)
EXEC sp_rename 'dbo.PN_Test.PNUser1',  'user_field_1',  'COLUMN';
EXEC sp_rename 'dbo.PN_Test.PNUser2',  'user_field_2',  'COLUMN';
EXEC sp_rename 'dbo.PN_Test.PNUser3',  'user_field_3',  'COLUMN';
EXEC sp_rename 'dbo.PN_Test.PNUser4',  'user_field_4',  'COLUMN';
EXEC sp_rename 'dbo.PN_Test.PNUser5',  'user_field_5',  'COLUMN';
EXEC sp_rename 'dbo.PN_Test.PNUser6',  'user_field_6',  'COLUMN';
EXEC sp_rename 'dbo.PN_Test.PNUser7',  'user_field_7',  'COLUMN';
EXEC sp_rename 'dbo.PN_Test.PNUser8',  'user_field_8',  'COLUMN';
EXEC sp_rename 'dbo.PN_Test.PNUser9',  'user_field_9',  'COLUMN';
EXEC sp_rename 'dbo.PN_Test.PNUser10', 'user_field_10', 'COLUMN';