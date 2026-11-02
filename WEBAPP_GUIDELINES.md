# Web app guidelines

## Existing stack and boundaries

- Use React/TypeScript/Vite and the existing CSS/font design. Keep this SPA embedded in the Go binary; do not adopt the starter's Next.js, SSR, or Python defaults.
- Route requests through `web/src/api.ts`. Preserve same-origin `/api` requests, configured base-path hosting, timeouts, and useful error messages. Provider credentials never enter browser code.
- Keep feature logic in its existing feature module. Use local state and pure transformations where adequate; adding a query library, routing library, import alias, or schema package requires an actual need.
- Preserve existing relative imports and component naming. Type public API shapes; validate uncertain external inputs at the server boundary.

## UI behavior

- Every asynchronous screen/panel distinguishes loading, success, empty/filter-empty, failure, stale, and partial data where applicable. Do not show zero or an empty-state message while a request is pending.
- Ignore late responses after scope changes or unmounts. Clear data when identity/authorization changes. Present refreshing retained data as stale when refresh fails.
- Keep filters and supported deep links consistent with the URL. A hidden control does not replace backend authorization.
- Display source evidence separately from operator decisions and optional model judgments. Use fixed labels or source annotations; do not add generated narrative.
- Keep urgent incidents visible. Namespace grouping, search, and collapsed details should reduce noise without hiding critical conditions.
- Use explicit units, UTC date labels where aggregated in UTC, and accessible chart summaries. Missing resource/coverage observations remain unknown. Ready pods, ready endpoints, and successful probes are different observations.
- Use semantic buttons, labels, keyboard-operable disclosure/dialog controls, visible focus, and reduced-motion support. Tables may scroll inside their panel; avoid whole-page horizontal overflow on mobile.

## Verification

Use [TESTING.md](TESTING.md) for browser and data-semantics checks. Public screenshots must use synthetic data. Preserve existing silence workflows, tenant read-only behavior, and base-path navigation when changing shared UI.
