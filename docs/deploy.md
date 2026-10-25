# Deploy: nx-bcat

## Running

| env | default | |
|---|---|---|
| `NEWS_ADMIN_URL` | (required) | news admin listener, e.g. `http://openpak-news-experiment:20043` |
| `NEWS_ADMIN_KEY` | | its `X-Internal-Key` |
| `NEWS_DATASET_URL` | | bcatserve, e.g. `http://openpak-bcat:20044`; unset = no native game BCAT (empty lists) |
| `NX_INTERNAL_KEY` | | the shared internal key: guards `/internal/*` here and is sent to bcatserve |
| `NX_PENNE_URL` / `NX_CORE_URL` | (one required) | who delivers the push (nx-penne, or nx-baas while it serves Penne) |
| `BCAT_SIGNING_KEY`, `BCAT_AES_KEY` | `/run/secrets/bcat-signing-key`, `/run/secrets/bcat-aes-key` | news's `NEWS_SIGNING_KEY`/`NEWS_AES_KEY` files |
| `NX_ADDR` | `:21000` | TLS for consoles (Traefik SNI passthrough) |
| `NX_API_ADDR` | `:20070` | plain HTTP: `/internal/bcat/sync`, `/internal/news/push`, forwarded console routes, `/openpak/v1/local/*` |
| `NX_BCAT_STATIC_DIR` | | a pre-signed tree (news's `-dir`, e.g. `cdn-v016`) served until news publishes its first build |
| `NX_DATA` | `/data` | `builds/` (the loaded tree, `state.json`) |
| `NX_CA_DIR` | `/data/ca` | `ca.pem`/`ca.key` that sign the listener's certificates |
| `NX_BCAT_DATA_ORIGIN` | `https://bcat-data-lp1.cdn.nintendo.net` | origin in native file URLs (159-char firmware limit) |
| `NX_BCAT_PUSH_WAIT_RANGE` | | `wait_range` seconds in each push; unset = the console default |
| `NX_PENNE_NEWS_RECEIVER` | `bcat` | the Penne Meta routing token (bcat listens under `bcat`, confirmed on Harbor 2026-10-16) |

## Cut-over (nothing here is applied anywhere)

1. Deploy `news` with the seed endpoints (it still signs and serves as before).
2. Start nx-bcat from `deploy/openpak-nx-bcat.container` with the **same** signing and AES key
   secrets. Check `journalctl`: `now serving build N`. Compare a topic list from nx-bcat
   (`curl -k --resolve ... https://bcat-list-lp1.cdn.nintendo.net:21000/...`) with the live one.
3. Set `NX_BCAT_SYNC_URL=http://openpak-nx-bcat:20070/internal/bcat/sync` on news and restart it
   (publishing no longer signs; nx-bcat does). Publish once; the log shows `nx-bcat synced`.
4. Set `NX_BCAT_URL=http://openpak-nx-bcat:20070` on nx-baas and restart it (bcat hosts forward).
5. Merge `deploy/traefik-nx-bcat.yml` into the Traefik file (above the news-experiment router for
   `bcat-topics-lp1`, which it replaces). nx-baas's forward is then only the safety net.

**Rollback**: remove the Traefik router and `NX_BCAT_URL`, unset `NX_BCAT_SYNC_URL` on news,
restart, then publish once: news signs and serves again (builds made while delegated have a seed
but no signed tree, so a plain restart would fall back to the static one).

## Building

`nx-common` is a sibling module with no remote: `go.mod` replaces it with `../nx-common` and
`vendor/` holds the copy the image build uses.

    GOWORK=off go vet ./... && GOWORK=off go test ./...

Releases are tags: `v*.*.*` publishes `ghcr.io/openpak/nx-bcat`.
`bcatcontainer`, `newsformat`, `newsexperiment` and `nativebcat` are ports of the news repo's
packages of the same names; a fix there applies here as a patch.
