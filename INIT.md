# Bootstrap refresh

Initial installation and bootstrap completed on 2026-11-02 using the upstream `scripts/install.sh`, then tailored for this repository. Normal work starts in [AGENTS.md](AGENTS.md); do not repeat bootstrap automatically.

For an explicitly requested refresh:

1. Read `go.mod`, `web/package.json`, its lockfile, Makefile, and `.github/workflows/`. CI and executable manifests are the source of truth for commands and versions.
2. Update the existing Project context block in `AGENTS.md`; do not add a duplicate. Keep it near twenty lines, focused on commands and non-obvious invariants.
3. Read [docs/DOCUMENTATION_BLUEPRINT.md](docs/DOCUMENTATION_BLUEPRINT.md). Update [docs/INDEX.md](docs/INDEX.md) to route to existing authoritative documents; do not generate empty template pages.
4. Adapt the coding, frontend, and testing guidelines to observed project conventions. Do not introduce Python, Next.js, plugins, global installations, or a different test framework from unrelated examples.
5. Keep one root instruction file while it covers this single Go/React application. Propose module files only when a real divergence cannot be expressed clearly in the root.
6. Check links, commands, and current behavior. Report what changed and any unverified claims. Follow the user's existing authorization for Git delivery.

Upstream source and adopted revision are recorded in [CONTRIBUTING.md](CONTRIBUTING.md). Future imports should be reviewed and merged, not blindly copied over these project-specific files.
