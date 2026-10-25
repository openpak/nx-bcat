# Notes: nx-bcat

The Switch's BCAT module for OpenPak, split out of [nx-baas](../../nx-baas) and the
[news](../../../../core/news) service, after the shop/Penne/parental-controls splits
(nx-baas `docs/service-split.md`).

## Status

nx-baas keeps its own `bcat.go`/`news.go` until the hosts move, as with the earlier splits.

Not moved yet: bcatserve still serves Harbor's JSON and, with `-signing-key`, its own native
routes; drop those flags once step 4 has run for a while.
