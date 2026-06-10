## Context

`grafana/sqlds` v5.1.1 (revision `6c09016`) is a thin layer between Grafana datasource plugins and `grafana-plugin-sdk-go/data/sqlutil`. The current public surface for macros is a re-export of `sqlutil.MacroFunc` (`macros.go:16`), interpolation is `sqlutil.Interpolate` (`macros.go:28-30`), and the datasource struct already accepts a handful of hook fields (`PreCheckHealth`, `PostCheckHealth`, `ResourceMiddleware` at `datasource.go:66-70`). There is no place to:

- attach plugin-level state (a metadata provider, a parser) so a macro can reach it without globals;
- pass a macro the `context.Context`, the macro's byte position in the source SQL, or the raw `backend.DataQuery.JSON`;
- substitute the interpolator with an AST-aware implementation;
- hook the lifecycle around interpolation (run setup once per query, mutate the rewritten SQL).

The Hydrolix fork has been the workaround. It added all of the above inline (see `hydrolix/sqlds` `0f83082`..`1738cf0`): a CTE-aware AST visitor in `interpolator.go`, a metadata provider for primary-key and column-type lookup, a custom `HDXQuery` model with filter fields, and a `MutateInterpolatedQuery` post-hook. The fork has been flagged in catalog review for moving security-relevant code (notably interpolation) out of the maintained, reviewed dependency, and for introducing a SQL-injection in its ad-hoc filter macro via unescaped `$$…$$` ClickHouse literals.

This design promotes the patterns the fork already proved out into reusable extension points in `sqlds`, so the same behavior can be supplied from outside the library.

## Goals / Non-Goals

**Goals:**
- Let plugins register custom macros that receive context, position, and plugin-level services.
- Let plugins replace the interpolator implementation per-datasource.
- Let plugins hook the interpolation lifecycle (pre for setup, post for rewrite).
- Preserve the existing public API; do not break datasources that don't opt in.
- Keep upstream `sqlutil` as the macro engine — `sqlds` adds the richer surface around it.

**Non-Goals:**
- Move macros, interpolators, or filter-condition builders for any specific backend into `sqlds`. Those live in plugin packages.
- Change `sqlutil.MacroFunc` upstream. The new signature lives in `sqlds` and is opt-in.
- Add backend-specific knowledge (ClickHouse parsers, dollar-quoted literals, AdHoc filter shapes) to `sqlds`.
- Driver-level parameter binding. Macros that need safe value interpolation emit single-quoted SQL literals with proper escaping (`'` → `\'`, `\` → `\\`) from inside their own implementation in the plugin package. Whether to move to bound parameters is a separate decision tracked outside this change.
- Generic backwards-compat shims for the existing fork shape. The HDX extension package is the consumer; it adapts to whatever upstream lands.

## Decisions

### Decision 1: `MacroContext` interface (additive new signature)

A new exported interface `MacroContext` carries everything a non-trivial macro needs. A new `ContextMacroFunc` type uses it; the legacy `sqlutil.MacroFunc` (still re-exported as the deprecated `sqlds.MacroFunc` alias) continues to work, and `sqlds` adapts between them at call time.

```go
type MacroContext interface {
    Context() context.Context
    Query() *sqlutil.Query        // parsed by sqlds, same as today
    QueryJSON() json.RawMessage   // raw DataQuery.JSON for plugin-side unmarshaling
    Pos() int                     // byte offset of the macro in Query.RawSQL
    Resolve(key any) (any, bool)  // plugin-attached services from the datasource
}

type ContextMacroFunc func(MacroContext, []string) (string, error)
```

`Resolve` reads from the datasource service registry (Decision 3). `Pos` is filled in by the interpolator when invoking the macro. Value-escaping for SQL fragments returned by a macro is the macro's responsibility — `sqlds` does not own a parameter binder or an escape helper; macros emit fully-formed SQL.

**Why interface instead of struct.** Keeps `sqlds` free to evolve the implementation (e.g. add `AST() any` later) without breaking existing macros; plugins use whichever methods they need.

**Why `ContextMacroFunc` instead of `MacroFunc`.** The package already exports `MacroFunc` as a deprecated alias for `sqlutil.MacroFunc`. Reusing that name for a structurally different type would be a breaking change; introducing a parallel `ContextMacroFunc` is additive and the legacy alias stays untouched.

**Alternatives considered.**
- *Extend `sqlutil.MacroFunc` upstream.* Out of scope for this change and gated by upstream review; would also force every caller to migrate.
- *Pass a concrete struct.* Locks the surface; growing it is a breaking change.

### Decision 2: `Interpolator` interface with default

```go
type Interpolator interface {
    Interpolate(ctx context.Context, ds *SQLDatasource, query *sqlutil.Query, rawJSON json.RawMessage) (string, error)
}
```

A default implementation (`DefaultInterpolator`) wraps `sqlutil.Interpolate` for legacy `sqlutil.MacroFunc` macros, plus the new `MacroFunc` for context-aware macros. Plugins assign their own via `SQLDatasource.Interpolator`.

The signature returns only `(string, error)` — no parameter list. Macros that need value interpolation emit escaped string literals themselves; `query.go` `Run` continues to call `db.QueryContext(ctx, sql, args...)` with whatever explicit `args` the caller passed, unchanged.

**Alternatives considered.**
- *Function pointer instead of interface.* Interface is more discoverable in Go code review and allows future composition (e.g. middleware-style wrapping).
- *Return `[]driver.NamedValue` for parameter binding.* Out of scope for this change — the working assumption is that macros escape values inline (see Non-Goals).

### Decision 3: Datasource service registry

```go
func (ds *SQLDatasource) Register(key any, value any)
func (ds *SQLDatasource) Resolve(key any) (any, bool)
```

Keys are typed (e.g. an unexported type per service in the plugin package) to prevent collisions across plugins. Storage is a `sync.Map` on `SQLDatasource`.

**Why on the datasource and not in `context.Context`.** The lifetime is the datasource, not the request. Putting it on `context.Context` would mean every request has to thread it through; putting it on the datasource means `MacroContext.Resolve` reads from a stable place. `context.Context` carries per-request state (the original `context.Context`, the SQL position) — the registry is for per-datasource state.

**Alternatives considered.**
- *Generic `Register[T]` API.* Go 1.25 supports generics on methods, but a typed key with `any` value keeps the registry decoupled from the consumer's type system and matches the `context.Context` idiom.
- *Constructor-time injection only.* Cleaner but less flexible; some services need to be created after the datasource is constructed.

### Decision 4: Interpolation hooks

Two hooks on `SQLDatasource`:

```go
PreInterpolate  func(ctx context.Context, query *sqlutil.Query, rawJSON json.RawMessage) error
PostInterpolate func(ctx context.Context, query *sqlutil.Query, sql string) (string, error)
```

`PreInterpolate` runs once per query before any macro fires (use case: parse the SQL into an AST and stash it via the service registry so every macro can read CTE info without re-parsing). `PostInterpolate` runs once on the rewritten SQL (use case: the existing `MutateInterpolatedQuery` design from fork commit `1738cf0` — final-pass adjustments after macro expansion).

**Why single-valued hooks instead of slices.** Plugins compose hooks themselves; making `sqlds` manage a chain adds API surface (`AddPreInterpolate`, ordering, error semantics). If multiple-hook composition becomes a pattern, add a helper later.

### Decision 5: Value escaping is the macro's responsibility

`sqlds` does not provide a parameter binder, a string escaper, or any other value-safety primitive. A macro that interpolates a user-supplied value MUST emit a safely-quoted SQL literal itself — typically a single-quoted string with `'` escaped as `\'` and `\` escaped as `\\` (the standard ClickHouse / MySQL convention). Escaping helpers live in the plugin package next to the macros that use them.

**Why this is OK.** Macros are already trusted to produce valid SQL fragments. Escaping is a small, well-understood pattern that the macro author owns end-to-end. Pulling it into `sqlds` would either require backend-specific knowledge (different escape rules per dialect) or a least-common-denominator helper that doesn't match any one backend cleanly.

**Why not parameter binding instead.** Considered, deliberately deferred. Adding it now would expand the API surface and the upstream-merge ask for a feature that the immediate consumers (the HDX extension package) can solve with inline escaping. Tracked as a possible follow-up; not blocking this change.

## Risks / Trade-offs

- [Service-registry abuse → API surface grows organically into a back door for behavior that should be a first-class feature] → Mitigation: document in the `plugin-services` spec the intended use cases (singletons attached at datasource construction; not per-request data). Reviewers can push back on plugins that abuse it.
- [`MacroContext.Pos()` changes meaning if macros are nested] → Mitigation: define `Pos` as the byte offset *in the source SQL before any rewrites at this macro position*. The interpolator already iterates macros from highest position to lowest (so earlier macros see unaffected text); this is documented in the `macro-extension-points` spec.
- [`PostInterpolate` ordering with hooks-of-hooks] → Mitigation: single-valued hook in this change. If composition becomes a pattern, add later.
- [Plugin authors get escaping wrong] → Mitigation: GoDoc on the new `MacroFunc` includes a canonical escape example for single-quoted literals; this change ships with a worked example in the documentation. Long-term safety net: a future change can add bound-parameter support without breaking what we ship here.
- [Backwards compatibility regressions if a datasource leaves `Interpolator` nil] → Mitigation: nil falls through to `DefaultInterpolator`, which is behaviorally equivalent to today's `sqlutil.Interpolate` path. Existing tests must continue to pass.
- [Increased API surface complicates upstream merges] → Mitigation: every new symbol is opt-in. No existing symbol changes signature. Document this in the proposal's Impact section.

## Migration Plan

This change is additive within `sqlds`. No migration is needed for existing datasource plugins. For the Hydrolix fork specifically:

1. Land this change on `grafana/sqlds`.
2. Create `hdx-grafana-sqlds-ext` consuming upstream `sqlds`.
3. Move the fork's `macros.go`, `metadata.go`, `interpolator.go` HDX-specific code into the new package, rewritten against `MacroContext`, `Interpolator`, `Register`/`Resolve`, and the pre/post hooks. Replace the `$$…$$` dollar-quoted literals with safely-escaped single-quoted literals inside the macro implementations.
4. Switch the Hydrolix Grafana plugin's import from `github.com/hydrolix/sqlds/v5` to `github.com/grafana/sqlds/v5` + `github.com/hydrolix/hdx-grafana-sqlds-ext`.
5. Retire the fork.

(That migration is separate work, not part of this change.)

## Open Questions

- Does `Interpolator.Interpolate` need to return per-macro diagnostics (line/column of error)? Today's `sqlutil.Interpolate` doesn't; deferring unless a consumer needs it.
- Should `sqlds` ship a single canonical escape helper (e.g. `EscapeSingleQuoted(string) string`) for the common ClickHouse/MySQL dialect, even though escaping is the macro's responsibility? Leaning yes as a small convenience, but it can land in a separate change.
