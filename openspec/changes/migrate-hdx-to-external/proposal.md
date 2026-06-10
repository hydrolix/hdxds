## Why

The `hydrolix/sqlds` fork diverged from `grafana/sqlds` v5.1.1 to add Hydrolix-specific behaviour: AST-aware interpolation, ClickHouse-flavoured time/date macros, an `AdHocFilterMacro`, a `MetaDataProvider` for caching primary keys and ad-hoc keys, an `HDXQuery` model with filter fields, and an `InterpolatedQueryMutator` driver hook. The fork carries this code at the cost of permanently lagging behind upstream and inheriting a SQL-injection liability ([macros.go's `AdHocFilterMacro` interpolates user-supplied values into ClickHouse `$…$` dollar-quoted literals](https://github.com/hydrolix/sqlds/issues)) that upstream does not have. With the extension points from [[add-extension-points]] in place, every HDX-specific behaviour can be expressed as a *consumer* of upstream sqlds rather than a fork of it.

This change is the **migration plan** that maps each HDX symbol on `1738cf0` to an extension surface, defines the public API of the new external package, and lays out the steps to retire the fork. **No code is moved by this change** — see Migration steps under Capabilities.

## What Changes

- Define a new external Go module `hdx-grafana-sqlds-ext` (path TBD — proposed `github.com/hydrolix/sqlds-hdx`) that imports upstream `grafana/sqlds` v5.1.1+ and registers HDX behaviour through extension points.
- Move into the new package: `macros.go` (all HDX macros + `Macros` registry), `metadata.go` (`MetaDataProvider` and PK/keys caching), `interpolator.go` (AST-aware interpolator + visitors), `HDXQuery` / `AdHocFilter` types, `models/settings.go` (HDX `PluginSettings`), and the `RoundTimeRange` helper.
- Adapt the HDX `MacroFunc` signature (`(context.Context, *HDXQuery, []string, parser.Pos, *MetaDataProvider) (string, error)`) into an upstream `ContextMacroFunc` via a shim that resolves `*HDXQuery` and `*MetaDataProvider` from the datasource registry.
- Map the driver-level `InterpolatedQueryMutator` hook to the datasource-level `PostInterpolate` hook on `*sqlds.SQLDatasource`. The driver interface keeps `MutateInterpolatedQuery` for plugin authors; the new package's bootstrap wires it through `PostInterpolate`.
- Replace fork's custom `Interpolator` field on `HydrolixDatasource` with an implementation of upstream's `Interpolator` interface registered via `ds.Interpolator = hdx.NewInterpolator(ds)`.
- **BREAKING (consumers of the fork only)**: Hydrolix plugins switch their import from `github.com/hydrolix/sqlds/v5` to `github.com/grafana/sqlds/v5` plus `github.com/hydrolix/sqlds-hdx`. The `sqlds.NewDatasource(driver)` call becomes `hdx.NewDatasource(driver)`. Type references (`sqlds.HDXQuery`, `sqlds.MetaDataProvider`, `sqlds.AdHocFilter`, `sqlds.MacroFunc`, `sqlds.PluginSettings`) become `hdx.HDXQuery`, etc.
- Retire `github.com/hydrolix/sqlds` once consumers complete the migration. The repository becomes either archived or a thin redirect.

## Capabilities

### New Capabilities

- `hdx-external-package`: Public API contract of the new external package — what types, functions, and registration helpers it exposes, and how it satisfies upstream extension points.
- `hdx-fork-retirement`: Migration plan for downstream Hydrolix plugins moving off the fork — module path changes, type renames, driver hook rewiring, retirement timeline, and rollback considerations.

### Modified Capabilities
<!-- None — this change does not modify upstream sqlds. All upstream surfaces consumed by the new package are already documented under add-extension-points. -->

## Impact

- **Affected repositories**: `hydrolix/sqlds` (this branch holds the design; no code moves on this branch), the new `hdx-grafana-sqlds-ext` package (to be created), and every downstream Hydrolix Grafana plugin that imports the fork.
- **No upstream `sqlds` changes**: the four extension points already added under [[add-extension-points]] (`plugin-services`, `macro-extension-points`, `pluggable-interpolator`, plus pre/post hooks) are sufficient to host every HDX symbol. No new upstream API is proposed by this change.
- **External dependencies on the new package**: `github.com/hydrolix/clickhouse-sql-parser` (AST parsing) and `github.com/jellydator/ttlcache/v3` (PK/keys cache). These dependencies leave upstream `sqlds` entirely.
- **Security**: the migration is *not* a fix for the SQL-injection liability in `AdHocFilterMacro`. The escape responsibility is documented under [[add-extension-points]]'s `macro-extension-points` spec. The new package's port of `AdHocFilterMacro` MUST emit safely-escaped single-quoted literals — this is called out as a task, not a design surface.
- **Out of scope**: parameter binding (driver-arg pass-through). Per prior decision under [[add-extension-points]], value escaping lives in macros.
