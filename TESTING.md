# Testing

Use behavioral checks proportional to the change. Prioritize authorization, calculations, persistence, and user-visible outcomes; do not write tests that only mirror implementation or add elaborate fixtures for trivial presentation changes.

## What makes a test valuable

- Prove a user-visible outcome or a public contract. Prefer integration tests for behavior crossing HTTP, authorization, storage, or workflow boundaries; use unit tests for calculations and invariants that add distinct coverage.
- Arrange the relevant state explicitly, perform the user/domain action, and assert the observable result near that action. Test names should describe behavior. Helpers should remove transport/setup noise without hiding the scenario or assertions.
- Use real internal services and temporary databases where practical. Mock external boundaries to exercise failures deterministically; do not mock away the behavior being tested.
- For frontend changes, prioritize user journeys and visible states over private hooks, reducer implementation details, incidental markup, or snapshot-heavy assertions. Use small page objects when shared interactions justify them, not as a mandatory layer.
- For permission tests, assert that authorized data is actually returned before checking redaction. For rejected writes, verify stored state remains unchanged as well as the HTTP status. Test names must describe scenarios actually exercised (for example, redirect protection needs a real redirect response).
- Cover meaningful failure, permission, conflict, and recovery cases alongside success. A regression test should fail when the bug is reintroduced. Avoid ceremonial tests, duplicated implementation logic, fragile timing, and coverage targets that reward assertion volume over confidence.

## Commands

Run from the repository root with Go's selected toolchain and Node.js 24 (minimum 22.12):

```bash
npm ci --prefix web
go test -race ./...
go vet ./...
npm test --prefix web
npm run format:check --prefix web
make build
npm exec --prefix web -- playwright install chromium
npm run test:e2e --prefix web
python3 scripts/enhanced-smoke.py --binary .build/alertmanager-ui
make helm-check
python3 scripts/compatibility.py --binary .build/alertmanager-ui --versions 0.27.0 0.34.1
```

CI's exact build and delivery steps live in `.github/workflows/ci.yml` and `release.yml`. Browser tests require the embedded binary and Docker for the real-Alertmanager test. Compatibility tests use isolated Docker containers. Helm tests require Helm 3. Do not point mutation tests at production.

Focused checks:

```bash
go test ./internal/server -run TestAnalytics
npm test --prefix web -- src/cluster.test.ts
npm run test:e2e --prefix web -- enhanced.spec.ts
```

## Backend

Use Go's testing package, HTTP test servers, and temporary real SQLite databases. Describe the initial condition, action, and observable contract clearly. Test external providers with bounded fake HTTP servers; avoid mocking internal persistence. Important paths include cross-namespace rejection, cache scope, missing/stale metrics, overlaps and recurrence, optimistic conflicts, restart/migration/backup behavior, model failures/budgets, and webhook rescheduling/retries.

The portability test executes PromQL with `promtool` when installed and explicitly skips that check otherwise. Report that distinction. Use synthetic alternate job names and missing exporters to expose cluster-specific assumptions.

## Frontend and browser

Use Vitest for calculation/state rules and Playwright for actual interactions. Prefer role/label locators and assertions near the action. Reuse small helpers when helpful; page objects and BDD libraries are not mandatory. Mock external network boundaries for controlled failure/empty/loading states, and retain the real Alertmanager silence workflow test.

Cover the changed flow plus an adjacent existing flow, tenant read-only presentation, and relevant loading/error/empty/partial states. Check mobile layout and keyboard operation for meaningful UI changes. Existing screenshot flows use synthetic fixtures; `UPDATE_SCREENSHOTS=1` deliberately updates public images.

## Evidence and reporting

Record the tested commit, commands, environment, and results in the PR. Link synthetic screenshots and relevant checks. Distinguish local checks from GitHub CI and read-only cluster observations from deployment. Never infer live Jev accuracy, human notification, or production compatibility from mocked transport tests.
