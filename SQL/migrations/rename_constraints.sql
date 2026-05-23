-- rename_constraints.sql
-- Renames auto-generated constraint names to explicit, predictable names.
-- Safe to run on a live DB — sp_rename on constraints does not lock data.
-- Run against the PartsMaster database (all tables here are in that DB).
--
-- NOTE: Two PN.PNUser1* constraints are ambiguous from the name alone.
-- Run the lookup query below first to confirm which hash maps to PNUser1 vs PNUser10.

-- LOOKUP — run this first, verify before proceeding:
SELECT dc.name AS constraint_name, c.name AS column_name
FROM sys.default_constraints dc
JOIN sys.columns c ON c.object_id = dc.parent_object_id AND c.column_id = dc.parent_column_id
WHERE dc.name IN ('DF__PN_Test__PNUser1__19FFD4FC', 'DF__PN_Test__PNUser1__22951AFD');

-- ============================================================
-- PN — PN_Test rename artifacts
-- ============================================================
EXEC sp_rename N'dbo.DF__PN_Test__PNActiv__2942188C', N'DF_PN_PNActive',        N'OBJECT';
EXEC sp_rename N'dbo.DF__PN_Test__PNCurre__284DF453', N'DF_PN_PNCurrentCost',   N'OBJECT';
EXEC sp_rename N'dbo.DF__PN_Test__PNDate__23893F36',  N'DF_PN_PNDate',           N'OBJECT';
EXEC sp_rename N'dbo.DF__PN_Test__PNDateM__2B2A60FE', N'DF_PN_PNDateModified',  N'OBJECT';
EXEC sp_rename N'dbo.DF__PN_Test__PNDetai__162F4418', N'DF_PN_PNDetail',         N'OBJECT';
EXEC sp_rename N'dbo.DF__PN_Test__PNFILID__2759D01A', N'DF_PN_PNFILIDPrimary',  N'OBJECT';
EXEC sp_rename N'dbo.DF__PN_Test__PNFILLi__2665ABE1', N'DF_PN_PNFILLinks',      N'OBJECT';
EXEC sp_rename N'dbo.DF__PN_Test__PNNotes__190BB0C3', N'DF_PN_PNNotes',          N'OBJECT';
EXEC sp_rename N'dbo.DF__PN_Test__PNPOLin__2A363CC5', N'DF_PN_PNPOLinks',       N'OBJECT';
EXEC sp_rename N'dbo.DF__PN_Test__PNQty__247D636F',   N'DF_PN_PNQty',            N'OBJECT';
EXEC sp_rename N'dbo.DF__PN_Test__PNReqBy__18178C8A', N'DF_PN_PNReqBy',         N'OBJECT';
EXEC sp_rename N'dbo.DF__PN_Test__PNStatu__17236851', N'DF_PN_PNStatus',         N'OBJECT';
EXEC sp_rename N'dbo.DF__PN_Test__PNTitle__153B1FDF', N'DF_PN_PNTitle',          N'OBJECT';
EXEC sp_rename N'dbo.DF__PN_Test__PNType__1352D76D',  N'DF_PN_category',         N'OBJECT';
EXEC sp_rename N'dbo.DF__PN_Test__revisio__1446FBA6', N'DF_PN_revision',         N'OBJECT';
EXEC sp_rename N'dbo.DF__PN_Test__PNUser2__1AF3F935', N'DF_PN_PNUser2',          N'OBJECT';
EXEC sp_rename N'dbo.DF__PN_Test__PNUser3__1BE81D6E', N'DF_PN_PNUser3',          N'OBJECT';
EXEC sp_rename N'dbo.DF__PN_Test__PNUser4__1CDC41A7', N'DF_PN_PNUser4',          N'OBJECT';
EXEC sp_rename N'dbo.DF__PN_Test__PNUser5__1DD065E0', N'DF_PN_PNUser5',          N'OBJECT';
EXEC sp_rename N'dbo.DF__PN_Test__PNUser6__1EC48A19', N'DF_PN_PNUser6',          N'OBJECT';
EXEC sp_rename N'dbo.DF__PN_Test__PNUser7__1FB8AE52', N'DF_PN_PNUser7',          N'OBJECT';
EXEC sp_rename N'dbo.DF__PN_Test__PNUser8__20ACD28B', N'DF_PN_PNUser8',          N'OBJECT';
EXEC sp_rename N'dbo.DF__PN_Test__PNUser9__21A0F6C4', N'DF_PN_PNUser9',          N'OBJECT';
-- Verify the lookup result before running these two:
EXEC sp_rename N'dbo.DF__PN_Test__PNUser1__19FFD4FC', N'DF_PN_PNUser1',          N'OBJECT';
EXEC sp_rename N'dbo.DF__PN_Test__PNUser1__22951AFD', N'DF_PN_PNUser10',         N'OBJECT';
EXEC sp_rename N'dbo.DF__PN__price_id__231F2AE2',     N'DF_PN_price_id',         N'OBJECT';
EXEC sp_rename N'dbo.DF__PN__has_bom__4F87BD05',     N'DF_PN_has_bom',           N'OBJECT';
EXEC sp_rename N'dbo.UQ__PN_Test__EC08A3D50081A4CA',  N'UQ_PN_PNPartNumber',     N'OBJECT';

-- ============================================================
-- Other tables
-- ============================================================
EXEC sp_rename N'dbo.DF__app_confi__updat__32EB7E57', N'DF_app_config_updated_at',               N'OBJECT';
EXEC sp_rename N'dbo.DF__CN__CNDateModifi__59463169',  N'DF_CN_CNDateModified',                   N'OBJECT';
EXEC sp_rename N'dbo.DF__FIL__is_active__538D5813',    N'DF_FIL_is_active',                       N'OBJECT';
EXEC sp_rename N'dbo.df_order',                        N'DF_FIL_order_id',                        N'OBJECT';
EXEC sp_rename N'dbo.DF__LNK__LNKChoice__60E75331',    N'DF_LNK_LNKChoice',                       N'OBJECT';
EXEC sp_rename N'dbo.DF__LNK__LNKCurrentC__62CF9BA3',  N'DF_LNK_LNKCurrentCost',                  N'OBJECT';
EXEC sp_rename N'dbo.DF__LNK__LNKUse__5EFF0ABF',       N'DF_LNK_LNKUse',                          N'OBJECT';
EXEC sp_rename N'dbo.DF__logs__date_logge__5B2E79DB',  N'DF_logs_date_logged',                    N'OBJECT';
EXEC sp_rename N'dbo.DF__named_que__activ__070CFC19',  N'DF_named_queries_active',                N'OBJECT';
EXEC sp_rename N'dbo.DF__named_que__resul__0618D7E0',  N'DF_named_queries_result_type',           N'OBJECT';
EXEC sp_rename N'dbo.UQ__named_qu__72E12F1B32187951',  N'UQ_named_queries_name',                  N'OBJECT';
EXEC sp_rename N'dbo.DF__PO__date_modifie__5575A085',  N'DF_PO_date_modified',                    N'OBJECT';
EXEC sp_rename N'dbo.DF__PO__internal_not__5C57A83E',  N'DF_PO_internal_notes',                   N'OBJECT';
EXEC sp_rename N'dbo.DF__PO__is_active__64B7E415',     N'DF_PO_is_active',                        N'OBJECT';
EXEC sp_rename N'dbo.DF__price__is_active__0C3BC58A',  N'DF_price_is_active',                     N'OBJECT';
EXEC sp_rename N'dbo.DF__price__price_ea__095F58DF',   N'DF_price_price_ea',                      N'OBJECT';
EXEC sp_rename N'dbo.DF__price__price_pac__0A537D18',  N'DF_price_price_pack',                    N'OBJECT';
EXEC sp_rename N'dbo.DF__release_n__date___0D64F3ED',  N'DF_release_notes_date_changed',          N'OBJECT';
EXEC sp_rename N'dbo.DF__release_n__notes__0C70CFB4',  N'DF_release_notes_notes',                 N'OBJECT';
EXEC sp_rename N'dbo.DF__release_n__show___0F4D3C5F',  N'DF_release_notes_show_users',            N'OBJECT';
EXEC sp_rename N'dbo.DF__release_n__usern__55DFB4D9',  N'DF_release_notes_username',              N'OBJECT';
EXEC sp_rename N'dbo.DF__release_n__versi__0B7CAB7B',  N'DF_release_notes_version',               N'OBJECT';
EXEC sp_rename N'dbo.DF__supplier__date_m__575DE8F7',  N'DF_supplier_date_modified',              N'OBJECT';
EXEC sp_rename N'dbo.DF__supplier___is_ac__51A50FA1',  N'DF_supplier_attachment_is_active',       N'OBJECT';
EXEC sp_rename N'dbo.DF__test_defi__chang__09E968C4',  N'DF_test_definition_history_changed_at',  N'OBJECT';
EXEC sp_rename N'dbo.DF__test_defi__chang__0ADD8CFD',  N'DF_test_definition_history_changed_by',  N'OBJECT';
