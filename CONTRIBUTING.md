# Contributing

Help make alert triage clearer without inventing information. Keep unfamiliar alerts visible, suppression explicit, and tenant authorization on the server.

## Development

Requires Go 1.26, Node.js 22.12 or newer, and npm. CI and container builds use Node.js 24 LTS. Docker is needed for browser/compatibility/container checks; Helm 3 is needed for chart checks.

```bash
make build
DEMO=true .build/alertmanager-ui
```

For frontend hot reload, run the server above and `cd web && npm run dev` in another terminal. Vite proxies `/api` to the local backend at port 8080. Use demo or a local read-only upstream while developing.

- `web/src`: React UI and pure prioritization logic.
- `internal/server`: same-origin API, authorization, upstream transport, embedded assets.
- `internal/config`: strict environment/YAML configuration.
- `internal/demo`: fictional examples, safe to publish.
- `charts`: Helm installation.

`make build` embeds `web/dist` into the Go binary. Generated assets, including `index.html`, are ignored by Git. The checked-in `internal/server/ui/placeholder.txt` keeps the embed directory present on a clean checkout; builds preserve it while replacing generated files. A bare `go build` compiles the API server but does not generate the frontend: use `make build` or Docker for a usable application.

## Verify a change

```bash
make test
make build
cd web
npx playwright install chromium
npm run test:e2e
cd ..
make helm-check
make compatibility
```

`make compatibility` starts isolated Docker containers for real Alertmanager releases and cleans them up. It never connects to a production cluster. Browser tests cover filtering, stale data, silence forms, tenant presentation, and mobile layout; screenshots use only demo data and are written to `web/test-results/screenshots`. Run `cd web && UPDATE_SCREENSHOTS=1 npm run test:e2e` to deliberately regenerate `docs/images`.

Backend tests verify authorization separately from presentation tests. A hidden control is never sufficient evidence of access control.

## Pull requests

Explain the user-visible change and how it was verified. For visual changes, include desktop/mobile screenshots from synthetic fixtures. For alert presets, include the exact alert names and explain why the category is appropriate. Do not classify alerts by guessing from namespace names.

Never commit production alert snapshots, tenant names, tokens, kubeconfig files, private review artifacts, or copied screenshots of live monitoring. The local prototype/review directories are intentionally excluded from Git and container build contexts.

## Releases

See [the release procedure](docs/deployment-cluster.md#publish-a-release). Successful CI after a merge to `main` automatically publishes the next patch release, images, and Helm chart. The Release workflow can also be dispatched for recovery. Production deployment remains an operator action.

The module selects Go 1.27.3 for local builds to match the patched Go 1.27 release line used by CI and containers. Let Go download the selected toolchain, or install an equally recent patched release.

## Agent workflow

Start with [AGENTS.md](AGENTS.md), then use [docs/INDEX.md](docs/INDEX.md) for task-specific context. The core guidance was installed with the upstream `scripts/install.sh` from [aai-labs/ai-starter](https://github.com/aai-labs/ai-starter/tree/fb8d0ace8367edc6d99ba2f03c3b914dc90bbab4) and bootstrapped on 2026-11-02. It is tailored to this Go/React application; optional plugins and skills were not installed. Python/Next.js defaults and unrelated starter history were removed.
