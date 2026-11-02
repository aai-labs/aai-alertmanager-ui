# Documentation blueprint

Keep one authoritative home for each rule. `AGENTS.md` is the short working entry point; root guidelines cover coding, UI, and testing conventions; `docs/INDEX.md` routes tasks to maintained pages. README explains the product and installation for users. CHANGELOG records significant product changes, not upstream starter history.

Reuse existing feature/operational pages under `docs/` instead of duplicating them into a new directory scheme. Create a glossary only if terminology needs a separate authoritative home. Current source, schemas, tests, and workflows own exact implementation details.

Document consequential decisions under `docs/adr/YYYY-MM-DD-slug.md` when rationale is supported by the agreed plan, maintainer statements, or prior records. Record the trade-off and consequence; do not invent historical reasons from code shape. Link decisions from relevant feature docs and the index.

Update behavior docs alongside contract changes, and update the index when pages move. Use synthetic public examples. Clearly distinguish current source, released images, local validation, and production deployment. Avoid placeholder pages and generic boilerplate that future agents would need to read without gaining task context.
