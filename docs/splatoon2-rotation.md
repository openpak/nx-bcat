# Splatoon 2 rotation generator

Splatoon 2 reads its stage rotation and Salmon Run shifts from BCAT: `vsdata/VSSetting_0.byaml`,
`vsdata/VSSetting_1.byaml` (identical) and `coopdata/CoopSetting.byaml`, in the title's bcatserve
dataset (`<datasets>/0100f8f0000a2000/`, see the news repo's `native-game-bcat.md`). nx-bcat signs
and serves that dataset. `cmd/s2rotation` generates those three files so the rotation does not
run out.

## What it does

    s2rotation -root <datasets> [-days 365] [-now RFC3339] [-n]      regenerate in place
    s2rotation -dump file.byaml                                      print a BYAML file as JSON

- **Template, not a copy.** Both files are mostly static game configuration around one phase list.
  The generator decodes the file already in the dataset, keeps every other key as it is, and
  replaces only `Phases` (VS) or appends to `Phases` + `MonthlyRewardGears` (co-op). The weapon-set,
  stage and gear pools come from the template. No game configuration is stored in this repository.
- **VS**: two-hour phases, each with Regular (turf war), Gachi and League. Gachi and League get
  different ranked rules (`cVar` Splat Zones, `cVlf` Tower Control, `cVgl` Rainmaker, `cVcl` Clam
  Blitz); every mode gets two distinct stages (ids 0-22) neither of which was in that mode's
  previous phase. Phases before 2026-11-02 stay as in the template; from then on a chain seeded
  per phase start time, so a later run reproduces what was already served and only extends the
  end. Phases older than 24 h are dropped.
- **Co-op**: existing shifts are never touched; new ones continue from the last (gap 42 h, 48 h one
  time in four, a shift lasting its gap minus 6 h, no stage twice in a row, an occasional rare
  weapon or all-random weapon set), one monthly reward gear per shift.
- `manifest.json` gets new sizes, hashes and a validity window (first VS phase to last VS phase end).
  Everything is written by temp file + rename; the manifest last.
- Datasets are read when bcatserve starts, so it must be restarted after a run.
  `deploy/openpak-s2rotation.{service,timer}` do this: monthly, they run the generator from the
  nx-bcat image (`/app/s2rotation`) and restart `openpak-bcat` only if the manifest changed.
  With `-days 365` the files always reach about a year ahead.

## Fixture provenance and licence

The first files in the dataset (2026-10-12) are a public community BCAT package the owner
authorised for testing: unmodified game data, no source code, **no licence stated**. It is not
OpenPak-authored and not bundled in any repository; the generator reads it only as a template on the
operator's machine and the public repository contains none of it. The `fesdata/` files (festival
text and textures) of that package are still in use and are not touched by this tool. The full
archive provenance and hashes are in the private research notes.

The BYAML reader/writer (`byml/`) is written from the public format description, not from any
third-party code.

## Not verified

The game has only been run against the template's phases. A console or Harbor must confirm that
generated phases (new stage pairs, rules, co-op shifts, the 4392-phase file) are accepted: check that
the lobby lists the current rotation after the first phase change and that a Salmon Run shift shows.
