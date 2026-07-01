-- Evaluate contacts that still carry a user_account_link value before the
-- column is dropped (#534 follow-up). Any rows returned are data that will be
-- lost when the column goes away — review them first.
--
-- Run against prod (contact) and, if in doubt, ArxDev too.
SELECT
    id,
    display_name,
    company_id,
    user_account_link,
    is_active
FROM contact
WHERE user_account_link IS NOT NULL
  AND LTRIM(RTRIM(user_account_link)) <> ''
ORDER BY display_name;

-- Quick count only:
-- SELECT COUNT(*) AS populated_rows FROM contact
-- WHERE user_account_link IS NOT NULL AND LTRIM(RTRIM(user_account_link)) <> '';
