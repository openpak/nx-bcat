# Changelog — nx-bcat

## v0.1.4 -- 2026-11-03

- Every HTTP request now writes one access line to stderr: `http <method> <host> <path> -> <status> <ms>ms` (shared `nx-common/httplog`, vendored). The query string, headers and bodies are never logged; before, the request line carried the full URI including the query. `/healthz` is skipped.

## v0.1.3 — 2026-11-01

- Add `cmd/s2rotation`, a Splatoon 2 rotation generator (VSSetting_*.byaml, CoopSetting.byaml)
  with its own BYAML reader/writer; shipped in the image as `/app/s2rotation`. Monthly timer and
  service units in `deploy/`. See `docs/splatoon2-rotation.md`.
