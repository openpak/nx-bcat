# Protocol: nx-bcat

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

A hostname that is not `bcat-*` is answered 421 and logged. An unmapped bcat route is logged and
404s; a topics or list request nothing publishes gets the signed-empty msgpack document nx-baas
always gave (`legacy.go`).
