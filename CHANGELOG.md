# Changelog — nx-bcat

## v0.1.5 -- 2026-11-05

- Native game data lists now carry `topic_id`, `service_status`, `na_required`, `test_distribution` and `required_application_version` before `directories`. The 23.0.1 bcat list parser fails the document at the start of `directories` unless the first four were read, and the sync then ends with 2122-0203 (seen on a real Switch with Tetris 99, whose title has no published data and got the empty list). Applies to every `nx_data_*` list, empty or not (openpak/prds#49).

## v0.1.4 -- 2026-11-03

- Every HTTP request now writes one access line to stderr: `http <method> <host> <path> -> <status> <ms>ms` (shared `nx-common/httplog`, vendored). The query string, headers and bodies are never logged; before, the request line carried the full URI including the query. `/healthz` is skipped.

## v0.1.3 — 2026-11-01

- Add `cmd/s2rotation`, a Splatoon 2 rotation generator (VSSetting_*.byaml, CoopSetting.byaml)
  with its own BYAML reader/writer; shipped in the image as `/app/s2rotation`. Monthly timer and
  service units in `deploy/`. See `docs/splatoon2-rotation.md`.
