## Why

Today, plugins that need behavior beyond what `grafana/sqlds` provides (custom macros that depend on metadata round-trips, AST-aware interpolation, query models with extra fields) have no clean way to inject that behavior. The practical workaround has been to fork the library, which moves security-relevant code (query interpolation, OAuth header construction, driver wiring) out of the maintained, reviewed dependency. The Hydrolix fork is a concrete example: it diverged precisely to add a CTE-aware interpolator, an `AdHocFilterMacro` that calls a metadata provider, ClickHouse-specific time macros, and a `MutateInterpolatedQuery` post-hook. That work could live outside `sqlds` if the library exposed the right hooks.

This change adds those hooks so a plugin can supply backend-specific behavior from its own package while keeping `sqlds` as the upstream, audited dependency.

## What Changes

- **Richer macro context.** Introduce a new `MacroFunc` signature that receives a `MacroContext` carrying `context.Context`, the parsed query, the raw `backend.DataQuery.JSON` (so plugins can unmarshal their own query model without `sqlds` knowing the shape), the macro's byte position in the source SQL, and accessors for plugin-attached services.
- **Pluggable `Interpolator` interface.** Promote the current regex-based interpolator into a default implementation behind an `Interpolator` interface; allow datasources to install a custom implementation (e.g. an AST-based one).
- **Datasource service registry.** Add `Register(key, value)` / `Resolve(key) (value, ok)` on the datasource so plugin-level singletons (a metadata provider, a parser, a cache) are reachable from macros and interpolators without globals.
- **Pre- and post-interpolation hooks.** Add `PreInterpolate(ctx, query)` and `PostInterpolate(ctx, query, sql)` extension points so plugins can do per-query setup (e.g. parse the SQL once, build a CTE map) and post-rewrite mutation (the existing `MutateInterpolatedQuery` design from the fork's commit 1738cf0).
- **Backwards compatibility.** The existing `MacroFunc` signature continues to work; the new signature is additive. Datasources that do not register services, hooks, or a custom interpolator behave exactly as today.
- **Out of scope: parameterized queries.** This change does not introduce a driver-arg binder. Macros that need safe value interpolation are expected to emit single-quoted SQL literals with proper escaping (`'` → `\'`, `\` → `\\`) from inside their own implementation in the plugin package. Whether to move to driver-level parameter binding is a separate decision tracked outside this change.

## Capabilities

### New Capabilities

- `plugin-services`: Datasource-scoped service registry that lets plugins attach singletons (metadata providers, parsers, caches) and retrieve them from macros, interpolators, and hooks without package-level globals.
- `macro-extension-points`: Richer macro signature exposing context, parsed query, raw query JSON, macro source position, and access to registered services — so backend-specific macros can be written outside `sqlds`.
- `pluggable-interpolator`: `Interpolator` interface with a default regex-based implementation; per-datasource override; pre- and post-interpolation hooks for setup and rewrite.

### Modified Capabilities

<!-- none — no existing specs in this repository -->

## Impact

- **Affected code (additive):** `macros.go` (new `MacroContext`, new `MacroFunc` variant, legacy wrapper), new `interpolator.go` (extracted `Interpolator` interface and default), `datasource.go` (service registry, hook registration, interpolator override). No changes required in `query.go`.
- **Public API:** new types and methods. No existing exported symbol changes signature.
- **Dependencies:** none added.
- **Consumers:** datasource plugins gain new optional integration surface; plugins that don't use it are unaffected. Downstream effect: the Hydrolix fork's HDX-specific additions become movable to an external `hdx-grafana-sqlds-ext` package that imports `grafana/sqlds` and registers via these hooks — closing the catalog-review concern about forking a security-relevant library.
- **Risks:** API surface grows; need to draw a defensible boundary so service-registry usage doesn't become a backdoor for behavior that should be a first-class feature. Mitigated by documenting intended use cases in each spec.
