## Context

The `hydrolix/sqlds` fork diverged from `grafana/sqlds@6c09016` (release v5.1.1) through `1738cf0` ("add MutateInterpolatedQuery interface"). The diverging commit `0f83082` ("add lib hdx specific") added every Hydrolix-specific behaviour to the upstream package namespace, alongside a rename of `SQLDatasource` to `HydrolixDatasource` and `Connector` (struct) to `Connector` (interface) with a `HydrolixConnector` concrete type.

The change `add-extension-points` (already applied on this branch) introduced four extension surfaces on upstream `*SQLDatasource`: service registry (`Register`/`Resolve`), context-aware macros (`RegisterMacro`/`ContextMacroFunc`), pluggable interpolator (`Interpolator` field), and pre/post hooks (`PreInterpolate`/`PostInterpolate`). The four surfaces are sufficient to host every HDX symbol on `1738cf0` without further upstream changes. This document is the technical plan for the external package and the downstream migration.

### Inventory (HDX-flavoured surface on `1738cf0`)

| Symbol / file                                              | Lives on fork at        | Maps to                                                                                            |
| ---------------------------------------------------------- | ----------------------- | -------------------------------------------------------------------------------------------------- |
| `MacroFunc` (5-arg HDX signature)                          | `macros.go:27`          | Shim → upstream `ContextMacroFunc(MacroContext, []string) (string, error)`                         |
| `Macros` registry + all time/date/interval macros          | `macros.go:437`         | Register each via `ds.RegisterMacro(name, shim(hdxFunc))` in new package's `Bootstrap`             |
| `AdHocFilterMacro`, `buildArrayCondition`, `escapeWildcard`, … | `macros.go:190+`    | Move verbatim into new package, register as above. **Add proper single-quoted literal escaping** at port time. |
| `MetaDataProvider` + `ttlcache` PK/keys                    | `metadata.go:25+`       | Move verbatim; holds `*sqlds.SQLDatasource` reference for `QueryData` calls                        |
| `Interpolator` struct + AST visitors + `getMacroMatches`   | `interpolator.go`       | Implements upstream `sqlds.Interpolator` interface; assigned via `ds.Interpolator = …`             |
| `HDXQuery`, `AdHocFilter`, `WithSQL`, `GetHdxQuery`        | `interpolator.go:267+`  | Move into new package; `PreInterpolate` parses raw JSON into `*HDXQuery` and stashes on registry   |
| `MacroId`, `CTE`, `macroVisitor`, `tableVisitor`, `queryVisitor`, `GetMacroCTEs` | `interpolator.go:170+` | Move verbatim; used internally by HDX interpolator                                                 |
| `RoundTimeRange`                                           | `interpolator.go:185`   | Move; helper used by HDX interpolator                                                              |
| `InterpolatedQueryMutator` driver interface                | `driver.go`             | Keep driver interface in new package; new package's bootstrap wires its `MutateInterpolatedQuery` call into upstream `ds.PostInterpolate` |
| `PluginSettings` (Hydrolix-flavoured config)               | `models/settings.go`    | Move verbatim into `hdx/models` subpackage                                                         |
| `QueryArgSetter`, `QueryErrorMutator`                      | already upstream        | No move — already available via upstream `Driver` interface                                        |
| `HealthChecker.Pre/PostCheckHealth`                        | already upstream        | No move — already available                                                                        |
| Removed files (`.github/*`, etc.)                          | n/a                     | Not migrated; out of scope                                                                         |
| Upstream `completion.go` (`Completable`, `/schemas`, `/tables`, `/columns`) | stays on upstream       | Plugin driver doesn't implement `Completable`; stubs return 400 on never-called paths. No interference. Plugin's `RegisterRoutes(map)` migrates to upstream's `ds.CustomRoutes = map`. |

### Constraints

- **Backwards compatibility on upstream** must remain intact — the new package is a pure consumer.
- **Security**: the port of `AdHocFilterMacro` MUST emit safely-escaped single-quoted literals. The current fork implementation interpolates user values into ClickHouse `$…$` dollar-quoted literals and is therefore vulnerable to SQL injection. **The port is not a verbatim copy of that function**; escaping is mandatory.
- **No new upstream API**: every gap turned out to be either pre-existing or solvable via plugin-side wiring through the registry / context.

## Goals / Non-Goals

**Goals:**

- Plan the move of every HDX symbol off the fork without losing functionality (modulo the `AdHocFilterMacro` security fix, which is gained).
- Define the public API of the new external package — what types, constructors, and registration helpers it exposes.
- Define the migration path for downstream Hydrolix Grafana plugins: import-path changes, type renames, driver hook rewiring.
- Confirm zero upstream `sqlds` changes are needed (already validated in inventory).

**Non-Goals:**

- Code movement. This change is **design only**; the new package is not created and the fork is not modified.
- Parameter-binding / driver-arg pass-through. Excluded per the same decision as in [[add-extension-points]] — value escaping lives in macros.
- Retirement of the `hydrolix/sqlds` repo. That's a downstream activity tracked under `hdx-fork-retirement`.
- Re-introduction of the `Completable` interface on the Hydrolix driver. Plugin already serves typed completion via its own routes (`/ast`, `/interpolate`, `/macroCTE` plus its frontend Monaco provider); upstream's `Completable` returns plain `[]string` and is strictly less useful. The fork's removal of `completion.go` was dead-code cleanup, not a forced workaround — see Decision 7 for the resulting route-registration migration step.

## Decisions

### Decision 1: PoC package layout — subdirectory under this repo's `go.mod`

**Scope of this change**: a proof-of-concept implementation under `./hdx` in this repository, using the repo's existing root `go.mod` (module `github.com/grafana/sqlds/v5` — this branch is upstream-aligned, forked off `6c09016` / release 5.1.1). Import path is `github.com/grafana/sqlds/v5/hdx`. The PoC's purpose is to **demonstrate that the four extension points are sufficient** to host HDX features — not to ship the production replacement package. Production retirement of the fork remains under [[hdx-fork-retirement]] and continues to target a separate repo.

The PoC takes on the HDX dependencies (`github.com/hydrolix/clickhouse-sql-parser`, `github.com/jellydator/ttlcache/v3`) in the root `go.mod`. This pollutes upstream's dependency graph for the duration of the PoC and is the price of the in-repo demonstration. When the production package moves to its own repository, the dependencies leave with it.

`./hdx` SHALL NOT modify any file outside itself. The hard constraint of the PoC is: zero new features in upstream sqlds API, zero edits to files outside `./hdx`.

**Alternatives considered:**

- Separate Go module at `github.com/hydrolix/sqlds-hdx`. Rejected for the PoC — adds repo-creation latency and platform/eng confirmation that the user does not want to block on for a demonstration. This remains the **production** target; see Decision 1.5.
- Nested subdirectory with its own `go.mod`. Rejected for the PoC — the user explicitly asked for the root `go.mod`. Otherwise this would have been the production-equivalent layout that keeps dependencies isolated.

### Decision 1.5: Production layout still aims for a separate repo

The PoC layout is intentionally cheap and temporary. Once the PoC validates the approach end-to-end, the production migration follows the original plan in [[hdx-fork-retirement]]: a separate `github.com/hydrolix/sqlds-hdx` module with its own dependency graph and release cadence. The PoC's code can then be lifted into that repo with import-path rewrites only.

### Decision 2: Datasource entry point

The new package exposes `func NewDatasource(driver sqlds.Driver) *sqlds.SQLDatasource`. It returns the upstream `*SQLDatasource` after attaching:

1. The custom `Interpolator` (HDX AST-aware).
2. `PreInterpolate` hook that parses `req.JSON` into `*HDXQuery` and `Register`s it on the datasource registry keyed by an unexported `hdxQueryKey{}`.
3. `PostInterpolate` hook that calls the driver's `MutateInterpolatedQuery` if implemented (preserving the fork's hook semantics).
4. `MetaDataProvider` instance registered under unexported `metadataProviderKey{}`.
5. Each HDX macro registered via `ds.RegisterMacro(name, shim(hdxFunc))`.

Plugin authors switch from `sqlds.NewDatasource(driver)` to `hdx.NewDatasource(driver)`. **No further plugin changes** to register macros — that happens inside the new package's bootstrap.

**Alternatives considered:**

- Return a wrapper type `hdx.Datasource` embedding `*sqlds.SQLDatasource`. Rejected — adds a type the plugin has to import everywhere; returning `*sqlds.SQLDatasource` keeps the plugin's existing `datasource.Manage` plumbing unchanged.
- Require plugin authors to call `ds.RegisterMacro` manually. Rejected — that defeats the migration's goal of "drop-in replacement except for one import path change".

### Decision 3: HDX `MacroFunc` → `ContextMacroFunc` shim

The HDX signature `func(ctx context.Context, q *HDXQuery, args []string, pos parser.Pos, mdp *MetaDataProvider) (string, error)` is wrapped by:

```go
func shim(fn HDXMacroFunc) sqlds.ContextMacroFunc {
    return func(mctx sqlds.MacroContext, args []string) (string, error) {
        q, _ := mctx.Resolve(hdxQueryKey{})
        mdp, _ := mctx.Resolve(metadataProviderKey{})
        return fn(mctx.Context(), q.(*HDXQuery), args, parser.Pos(mctx.Pos()), mdp.(*MetaDataProvider))
    }
}
```

This preserves the HDX macro authoring experience while flowing through the upstream extension point. The `parser.Pos` is byte offset–compatible with `MacroContext.Pos()`.

**Alternatives considered:**

- Change HDX `MacroFunc` signature to match upstream's. Rejected — would require touching every macro function body and breaks the HDX macro author idiom of "give me the query model directly".
- Drop the shim and rewrite every macro to use `mctx.Resolve` directly. Rejected — same downside, more invasive.

### Decision 4: `HDXQuery` parsing lives in `PreInterpolate`

The new package's `PreInterpolate(ctx, q *sqlutil.Query, raw json.RawMessage)`:

1. Unmarshals `raw` into a fresh `*HDXQuery`.
2. Copies `q.TimeRange`, `q.Interval` onto the `HDXQuery` (upstream `sqlutil.Query` already exposes both).
3. Resolves headers from `ctx` — they are stashed there by a `QueryDataMutator` that the new package's bootstrap also installs.
4. `Register`s the populated `*HDXQuery` under `hdxQueryKey{}`. Every macro invocation that follows reads it via `mctx.Resolve`.

Note: `PreInterpolate` fires **once per query**, before any macro. Macros that need to read a SQL-mutated copy (`q.WithSQL(currentRawSQL)`) can either compute it ad hoc inside the macro or call a helper that constructs the snapshot from the registry + current `MacroContext.Query().RawSQL`.

**Alternatives considered:**

- Parse HDXQuery inside each macro from `mctx.QueryJSON()`. Rejected — N JSON unmarshals per query is wasteful and risks per-macro drift if the parsing logic ever evolves.
- Add `HDXQuery` to upstream `*sqlutil.Query` as an opaque carrier. Rejected — upstream stays clean.

### Decision 5: Headers reach the metadata provider via context

The fork's `MetaDataProvider.executeQuery` reads `http.Header` from `*HydrolixDatasource.Connector.getInstanceSettings()` and `req.GetHTTPHeaders()`. The new package follows the same plumbing in a less invasive way:

1. `hdx.NewDatasource` installs a `QueryDataMutator` (driver hook) **OR** an on-the-fly `MutateQueryData` adapter wired before query execution.
2. The mutator stashes `req.Headers` on the request context via `context.WithValue(ctx, headersCtxKey{}, req.Headers)`.
3. `PreInterpolate` reads headers from the context and attaches them to the constructed `*HDXQuery`.
4. `MetaDataProvider.executeQuery` reads headers from its `*HDXQuery` arg or from `mctx.Context()`.

Plugin instance settings (used by `MetaDataProvider.getDefaultDatabase`) are also stashed on the registry once during `hdx.NewDatasource` setup so that the metadata provider does not need a back-reference to a Connector accessor.

**Alternatives considered:**

- Add an upstream `Headers` field on `sqlutil.Query`. Rejected — upstream-side change, and `context.Context` already carries request-scoped values idiomatically.
- Add an upstream accessor `*SQLDatasource.InstanceSettings(uid)`. Rejected — plugin-side state via registry is sufficient.

### Decision 6: Driver-level `InterpolatedQueryMutator` mapped to `PostInterpolate`

The new package's bootstrap inspects the supplied `sqlds.Driver`. If it satisfies the (new-package–defined) `InterpolatedQueryMutator` interface, the bootstrap sets:

```go
ds.PostInterpolate = func(ctx context.Context, q *sqlutil.Query, sql string) (string, error) {
    ctx2, mutated := mutator.MutateInterpolatedQuery(ctx, sql)
    _ = ctx2 // retained for future use; PostInterpolate currently has no ctx-return slot
    return mutated, nil
}
```

The driver-level hook continues to exist for plugin authors who already implement it. Upstream gains nothing new; the wiring is in the new package.

**Caveat**: `PostInterpolate` does not currently return a new `context.Context`. If a downstream driver depended on `MutateInterpolatedQuery` returning a derived context, that derivation is lost. As of `1738cf0` no Hydrolix driver does this — the returned context is discarded by the fork's own `handleQuery`. If a future need emerges, upstream `PostInterpolate`'s signature could grow a `(context.Context, string, error)` return; out of scope for this change.

### Decision 7: Route registration migrates from fork's `RegisterRoutes(map)` to upstream's `ds.CustomRoutes` field

Upstream `*SQLDatasource` exposes a `CustomRoutes map[string]func(http.ResponseWriter, *http.Request)` struct field. During `ds.NewDatasource(ctx, settings)` the upstream code builds a fresh `http.ServeMux`, registers the `Completable` stub routes (`/schemas`, `/tables`, `/columns`), then iterates `CustomRoutes` and rejects any entry that collides with the stub paths. The combined mux is wrapped via `httpadapter.New` and assigned to `ds.CallResourceHandler`.

The fork replaced this with a method-style API: `(ds *HydrolixDatasource) RegisterRoutes(customRoutes map)` builds a *fresh* mux containing only the supplied custom routes and directly sets `ds.CallResourceHandler`, sidestepping upstream's collision check and `Completable` stubs entirely.

For migration the plugin SHALL switch from:

```go
ds.RegisterRoutes(api.Routes(ds))
```

to:

```go
ds.CustomRoutes = api.Routes(ds)
```

assigned **before** `ds.NewDatasource(ctx, settings)`. Plugin routes (`/ast`, `/interpolate`, `/macroCTE`) do not collide with the `Completable` stubs (`/schemas`, `/tables`, `/columns`), so upstream's collision check passes. The stub routes return HTTP 400 `ErrorNotImplemented` when called (because the plugin's driver does not implement `Completable`), but nothing in the plugin frontend calls them — they are dead routes, not functional interference.

**Alternatives considered:**

- Have `hdx.NewDatasource(driver)` accept a `customRoutes` parameter and forward to `ds.CustomRoutes`. Rejected for the public bootstrap — keeps the signature trivial; plugins can set `ds.CustomRoutes` themselves before the second-stage `ds.NewDatasource(ctx, settings)` call. May be reconsidered if multiple Hydrolix plugins adopt the package and a sugar wrapper proves useful.
- Restore the fork's mux-replacement semantics in the new package. Rejected — fights upstream's collision contract and silently drops the (admittedly unused) `Completable` stubs, which is unnecessary complexity for zero behavioural gain.

### Decision 8: Escaping in `AdHocFilterMacro`

The port of `AdHocFilterMacro` MUST emit single-quoted literals with `'` escaped as `\'` and `\` escaped as `\\`. The fork's current implementation emits `$<value>$` (ClickHouse dollar-quoted literals) which have no defined escape sequence — this is the vulnerability that prompted the migration. The port is the opportunity to fix it.

The new package SHALL provide an internal `escapeSQLLiteral(s string) string` helper and use it from every macro that emits user-supplied string values (`AdHocFilterMacro`, any future variants). Tests SHALL cover injection vectors including embedded single quotes, backslashes, and Unicode characters that some clients normalise.

## Risks / Trade-offs

- **[Risk] HDX macros that depend on `*HDXQuery.WithSQL(currentRawSQL)` semantics** → Mitigation: the registry resolves the **original** `*HDXQuery`; macros that need the in-flight SQL can read `mctx.Query().RawSQL`. Validate this against `AdHocFilterMacro`, `TimeFilter`, and `TimeFilterMs` during the port — those three call `getPK(ctx, query.RawSQL, pos, …)` and thus need the up-to-date RawSQL passed through.
- **[Risk] AST parser version drift** → Mitigation: pin `github.com/hydrolix/clickhouse-sql-parser` in the new package's go.mod. Upstream sqlds stays unaware of it.
- **[Risk] Plugin authors miss the import switch and silently keep using the fork** → Mitigation: archive `hydrolix/sqlds` once migrations land. A deprecation notice in the fork's README is the first step.
- **[Trade-off] Plugin-side registry plumbing** → The fork hid `MetaDataProvider` behind `HydrolixDatasource.Connector.getInstanceSettings()`; the new package surfaces it via an unexported registry key. The trade is more setup wiring in `hdx.NewDatasource` (one-time) in exchange for keeping upstream sqlds free of HDX concerns.
- **[Trade-off] PostInterpolate cannot return a derived context** → Acceptable today (no driver uses the return value); flag in design as a potential follow-up upstream change.
- **[Risk] Test coverage gap during migration** → Mitigation: the port lifts `macros_test.go`, `metadata_test.go`, `interpolator_test.go` verbatim into the new package, ensuring behavioural parity. Diffs that arise from the shim are captured as new tests under the new package.

## Migration Plan

### Phase 0 — This change (design only)

No code moves. This document and its specs are the contract.

### Phase 1 — Create the new package

In a new repository (proposed `github.com/hydrolix/sqlds-hdx`):

1. Initialise `go.mod` declaring module `github.com/hydrolix/sqlds-hdx`, depending on `github.com/grafana/sqlds/v5` at the version that includes [[add-extension-points]].
2. Copy `macros.go`, `metadata.go`, `interpolator.go`, `models/settings.go`, and their tests from `hydrolix/sqlds@1738cf0`. Adjust package names to `hdx` / `hdx/models`. Adjust imports.
3. Implement the shim (`HDXMacroFunc → sqlds.ContextMacroFunc`) and the registry keys (`hdxQueryKey{}`, `metadataProviderKey{}`, `headersCtxKey{}`, `instanceSettingsKey{}`).
4. Implement `hdx.NewDatasource(driver)` returning `*sqlds.SQLDatasource` with everything wired.
5. **Fix `AdHocFilterMacro` escaping** during the port (do not preserve the dollar-quoted-literal bug).
6. Lift the fork's tests; add tests covering the shim and the escape fix.

### Phase 2 — Migrate downstream Hydrolix Grafana plugins (one per plugin)

For each plugin currently importing `github.com/hydrolix/sqlds`:

1. Update `go.mod`: replace `github.com/hydrolix/sqlds/v5` with `github.com/grafana/sqlds/v5` and add `github.com/hydrolix/sqlds-hdx`.
2. Replace `sqlds.NewDatasource(driver)` with `hdx.NewDatasource(driver)`.
3. Replace type references: `sqlds.HDXQuery` → `hdx.HDXQuery`, `sqlds.MetaDataProvider` → `hdx.MetaDataProvider`, `sqlds.AdHocFilter` → `hdx.AdHocFilter`, `sqlds.MacroFunc` (HDX-flavour) → `hdx.HDXMacroFunc`, `sqlds.PluginSettings` → `hdx/models.PluginSettings`.
4. Run plugin's test suite. End-to-end smoke test against a Hydrolix backend.
5. Cut a release.

### Phase 3 — Archive the fork

Once all downstream consumers are off `hydrolix/sqlds`:

1. Add a top-of-README deprecation notice pointing at `grafana/sqlds` + `hydrolix/sqlds-hdx`.
2. Tag a final `v5.x` release with the notice.
3. Archive the repository on GitHub. Keep it readable for git-archaeology purposes.

### Rollback

The migration is per-plugin. If Phase 2 of a given plugin fails (e.g., a missed code path), that plugin can keep using the fork until the issue is fixed in the new package. The fork stays available until Phase 3.

## Open Questions

1. **Final module path** — `github.com/hydrolix/sqlds-hdx` is a placeholder. Confirm with platform/eng before Phase 1.
2. **Versioning policy** — does the new package follow upstream sqlds's major version (track `v5.x`), or does it version independently? Suggest: track upstream major.
3. **CI** — the fork has no CI (deleted in `0f83082`). New package should set up CI from day one. Out of scope for this design doc, in scope for Phase 1.
4. ~~**`completion.go`** — restored on upstream, restored on new package, or dropped permanently?~~ **Resolved**: stays on upstream as-is. Verified against the plugin (`pkg/plugin/driver.go` doesn't implement `Completable`; `pkg/api/routes.go` registers `/ast`, `/interpolate`, `/macroCTE` which don't collide with upstream's `/schemas`, `/tables`, `/columns`). The unused stubs return HTTP 400 to never-called paths; no functional impact. Plugin migration replaces `ds.RegisterRoutes(map)` with `ds.CustomRoutes = map` (see Decision 7).
