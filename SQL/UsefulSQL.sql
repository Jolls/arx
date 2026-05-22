ALTER TABLE release_notes
ADD username varchar(64) default ' '

sp_rename 'Logs.ID', 'id', 'COLUMN';
sp_rename 'Logs.Username', 'username', 'COLUMN';
sp_rename 'Logs.Date', 'date_logged', 'COLUMN';
sp_rename 'Logs.SQLString', 'sql_string', 'COLUMN';
sp_rename 'Logs', 'logs';

ALTER TABLE SU
ADD default_contact INT NOT NULL DEFAULT '0'

USEFUL SCRIPTS:
'FOR DELETING UNWANTED POL ITEMS'
DELETE FROM POL WHERE POLPOID = 160
SELECT TOP 10 * FROM [dbo].[POL] ORDER BY POLID DESC

'FOR DELETING UNWANTED POID'
DELETE FROM PO WHERE POID = 160
SELECT TOP 10 * FROM [dbo].[PO] ORDER BY POID DESC

sp_rename 'PO.POID', 'id', 'COLUMN';