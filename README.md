# Switch BCAT Server

OpenPak's Switch BCAT Server.

| | |
| --- | --- |
| Network | Switch |
| Role | Signed News channels, native game BCAT and topic push for Switch consoles |
| Depends on | [`news`](https://github.com/openpak/news), [`nx-penne`](https://github.com/openpak/nx-penne) or [`nx-baas`](https://github.com/openpak/nx-baas) (push delivery) |
| Image | `ghcr.io/openpak/nx-bcat` |
| Licence | AGPL-3.0-only |

## What it serves

| Host | What |
| --- | --- |
| `bcat-list-<env>`, `bcat-topics-<env>` | Signed News channel lists and the topic catalog |
| `bcat-data-<env>.cdn.nintendo.net` | Signed News files and native game BCAT under `/bcat/` |
| NPNS `news` messages | Pushed to consoles subscribed to a topic when its list changes |

## Run

Runs as a container from `deploy/openpak-nx-bcat.container`. Environment variables and the
cut-over steps are in [deploy.md](docs/deploy.md).

Tests:

    GOWORK=off go vet ./... && GOWORK=off go test ./...

## Docs

- [Protocol](docs/protocol.md)
- [Evidence](docs/evidence.md)
- [Deploy](docs/deploy.md)
- [Notes](docs/notes.md)

## Licence

AGPL-3.0-only. See [LICENSE](LICENSE).
