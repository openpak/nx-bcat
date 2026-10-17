# nx-bcat

The Switch's BCAT module for OpenPak, split out of [nx-baas](../nx-baas) and the
[news](../../../core/news) service, after the shop/Penne/parental-controls splits
(nx-baas `docs/service-split.md`).

It owns what a console talks to:

- the hosts `bcat-list-<env>`, `bcat-topics-<env>`, `bcat-data-<env>.cdn.nintendo.net`;
- the **signed News channels** (`openpak_*` lists, topic catalog, detail, icon, archives,
  articles, the per-title topic lookup) -- signed here, with the only copy of the BCAT signing
  key and the News AES key;
- **native game BCAT**: `nx_data_<title>` lists and the signed files under `/bcat/` (signed
  plaintext, crypto type 0);
- **subscription delivery**: when a topic's list changes, an NPNS `news` message to every
  console subscribed to it, through the penne API (`/internal/penne/topic-messages`).

The **news** service keeps authoring, storage, the website JSON, the admin API, the channel
dataset and Harbor's public JSON. nx-bcat reads it over an internal API:

| call | answer |
|---|---|
| `GET news /admin/v1/bcat/current` | `{build, revision}` of the build news serves; build 0 = none yet |
| `GET news /admin/v1/bcat/builds/{n}/seed.tar` | the unsigned seed (`*.msgpack`, `icon.jpg`, `manifest.json`) |
| `GET bcatserve /internal/datasets/{title}` | the title's current dataset (paths, object keys, sizes, sha256), `Last-Modified`; 404 = none |
| `GET bcatserve /internal/objects/{key}` | the object's bytes |

News publishes a build and calls `POST /internal/bcat/sync` here (`NX_BCAT_SYNC_URL` in news).
nx-bcat pulls the seed, signs and verifies every container against the console key, swaps the
tree in atomically, diffs the topic list digests with the previous build and pushes the changed
topics. It also polls (`NX_BCAT_POLL`, 5 m) as the net under a lost call, and serves the last
stored build when news is down. A first load pushes nothing (every console fetches at boot).

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
| `NX_DATA` | `/data` | `builds/` (the loaded tree, `state.json`) |
| `NX_CA_DIR` | `/data/ca` | `ca.pem`/`ca.key` that sign the listener's certificates |
| `NX_BCAT_DATA_ORIGIN` | `https://bcat-data-lp1.cdn.nintendo.net` | origin in native file URLs (159-char firmware limit) |
| `NX_BCAT_PUSH_WAIT_RANGE` | | `wait_range` seconds in each push; unset = the console default |
| `NX_PENNE_NEWS_RECEIVER` | `bcat` | the Penne Meta routing token (bcat listens under `bcat`, confirmed on Harbor 2026-10-16) |

A hostname that is not `bcat-*` is answered 421 and logged. An unmapped bcat route is logged and
404s; a topics or list request nothing publishes gets the signed-empty msgpack document nx-baas
always gave (`legacy.go`).

## Cut-over (nothing here is applied anywhere)

nx-baas keeps its own `bcat.go`/`news.go` until the hosts move, as with the earlier splits.

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

Not moved yet: bcatserve still serves Harbor's JSON and, with `-signing-key`, its own native
routes; drop those flags once step 4 has run for a while.

## Building

`nx-common` is a sibling module with no remote: `go.mod` replaces it with `../nx-common` and
`vendor/` holds the copy the image build uses.

    GOWORK=off go vet ./... && GOWORK=off go test ./...

Releases are tags: `v*.*.*` publishes `ghcr.io/openpak/nx-bcat`.
`bcatcontainer`, `newsformat`, `newsexperiment` and `nativebcat` are ports of the news repo's
packages of the same names; a fix there applies here as a patch.
