# Database connection errors

When a connect from Settings fails, Arx shows a plain-language message, a stable code, and the raw
driver error under a collapsed **Details**. Search this page for the code to find causes and fixes.
The password and connection string are never shown or logged.

## db_auth_failed

**Symptom:** "The server rejected the password for user …" (SQLSTATE 28P01). Also what Postgres
returns for a user that does not exist under password authentication.

**Causes:** wrong password; password changed on the server; wrong user name.

**Fix:** re-enter the password in Settings, confirm the user name, or ask the database administrator
to reset the password.

## db_role_unknown

**Symptom:** "The server does not accept user …" (SQLSTATE 28000).

**Causes:** the login role does not exist, is disabled, or is not allowed to connect.

**Fix:** check the user name in Settings; ask the administrator to create or enable the role.

## db_database_unknown

**Symptom:** "The server has no database with that name" (SQLSTATE 3D000).

**Causes:** misspelled database name; wrong server; database not created yet.

**Fix:** check the database name in Settings (test mode uses the test database name).

## db_firewall_blocked

**Symptom:** "The server refused this computer's address" (server message mentions `pg_hba.conf`).

**Causes:** the server's access rules or firewall do not allow this computer's IP address.

**Fix:** ask the administrator to allow your current public IP (Azure: add a firewall rule; VPN or
location changes alter the IP).

## db_host_unreachable

**Symptom:** "Could not reach the database server".

**Causes:** wrong server name or port; DNS failure; no network or VPN; server down; connection
timed out; port blocked.

**Fix:** check the server address and port in Settings, your network/VPN, and that the server is up.

## db_tls_failed

**Symptom:** "The secure (TLS) connection to the server failed".

**Causes:** the server has TLS disabled (Arx always requires TLS); the port is not a Postgres
port; certificate problem.

**Fix:** enable TLS on the server, or check the address and port.

## db_connect_failed

**Symptom:** "Could not connect to the database" — anything not matched above.

**Causes:** varies, e.g. too many connections (SQLSTATE 53300) or a server-side restriction.

**Fix:** open **Details** for the raw error and look up its SQLSTATE in the PostgreSQL error-codes
appendix; if it recurs, report the code and raw error text.
