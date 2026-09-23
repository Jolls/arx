# #171 Stale outsider docs

- README.md:47 Go 1.22+ → Go 1.27+.
- README.md:63 and :72: DB password/credentials saved to per-user store `%APPDATA%\Arx\local.json`, not `config\local.json`.
- CONTRIBUTING.md:18 link `SQL/schema.md` → `SQL/SCHEMA.md`; add a "Licensing" line: contributions are accepted under AGPL-3.0 (resolved: no DCO).
- .env.example:10 drop `_Test` wording (test mode switches to the ArxDev database); lines 23-25 single `PORT=4568` default.
- start.ps1:16 print only `http://localhost:4568`; drop `$ip` line (line 10) since orphaned.
