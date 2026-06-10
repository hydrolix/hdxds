## ADDED Requirements

### Requirement: External `hdx` package SHALL expose `NewDatasource`

The external package SHALL provide a single bootstrap function `NewDatasource(driver sqlds.Driver) *sqlds.SQLDatasource` that returns an upstream `*sqlds.SQLDatasource` pre-configured with the HDX interpolator, all HDX macros, the metadata provider, and the pre/post interpolation hooks. Plugins migrating off the fork SHALL replace their `sqlds.NewDatasource(driver)` call with `hdx.NewDatasource(driver)` and need no further setup to obtain HDX behaviour.

#### Scenario: Plugin obtains an HDX-wired datasource

- **WHEN** a plugin calls `hdx.NewDatasource(driver)`
- **THEN** the returned `*sqlds.SQLDatasource` SHALL have `Interpolator` set to the HDX AST-aware implementation
- **AND** all HDX macros from the fork's `Macros` registry (`__timeFilter`, `__timeFilter_ms`, `__fromTime`, `__toTime`, `__fromTime_ms`, `__toTime_ms`, `__dateFilter`, `__dateTimeFilter`, `__timeInterval`, `__timeInterval_ms`, `__interval_s`, `__adHocFilter`) SHALL be registered via `ds.RegisterMacro`
- **AND** the `MetaDataProvider` SHALL be reachable from any macro via `mctx.Resolve(metadataProviderKey{})`

#### Scenario: Driver implements `InterpolatedQueryMutator`

- **WHEN** the supplied `driver` satisfies the `hdx.InterpolatedQueryMutator` interface
- **THEN** `hdx.NewDatasource` SHALL install a `PostInterpolate` hook that delegates to `driver.MutateInterpolatedQuery`
- **AND** the hook SHALL be invoked exactly once per query, on the fully interpolated SQL

#### Scenario: Driver does not implement `InterpolatedQueryMutator`

- **WHEN** the supplied `driver` does not satisfy `hdx.InterpolatedQueryMutator`
- **THEN** `hdx.NewDatasource` SHALL leave `PostInterpolate` nil
- **AND** interpolated SQL SHALL reach the database driver unchanged

### Requirement: HDX macro signature SHALL be preserved via a shim

The new package SHALL expose its own macro signature `HDXMacroFunc func(ctx context.Context, q *HDXQuery, args []string, pos parser.Pos, mdp *MetaDataProvider) (string, error)` matching the fork's `MacroFunc` exactly. Internally, a shim SHALL adapt `HDXMacroFunc` to upstream `sqlds.ContextMacroFunc` by resolving `*HDXQuery` and `*MetaDataProvider` from the datasource registry on each invocation.

#### Scenario: Shim translates upstream MacroContext to HDX arguments

- **WHEN** a macro registered via the shim is invoked during interpolation
- **THEN** the shim SHALL call `mctx.Resolve(hdxQueryKey{})` and pass the result as `*HDXQuery`
- **AND** SHALL call `mctx.Resolve(metadataProviderKey{})` and pass the result as `*MetaDataProvider`
- **AND** SHALL pass `mctx.Context()` as the macro's `context.Context` argument
- **AND** SHALL pass `parser.Pos(mctx.Pos())` as the macro's position argument

#### Scenario: HDXQuery missing from registry

- **WHEN** a macro is invoked but `hdxQueryKey{}` has not been registered
- **THEN** the shim SHALL return an error explaining that PreInterpolate did not populate the query model

### Requirement: HDXQuery SHALL be populated by `PreInterpolate`

The new package's `PreInterpolate` hook SHALL parse the raw `DataQuery.JSON` into a fresh `*HDXQuery`, populate `TimeRange` and `Interval` from the upstream `*sqlutil.Query`, attach `http.Header` from the request-scoped context, and register the result on the datasource registry under the unexported `hdxQueryKey{}`. The hook SHALL fire exactly once per query, before any macro expansion.

#### Scenario: PreInterpolate populates HDXQuery from raw JSON

- **WHEN** `PreInterpolate(ctx, q, raw)` runs
- **THEN** it SHALL `json.Unmarshal(raw, &hdxQuery)`
- **AND** SHALL set `hdxQuery.TimeRange = q.TimeRange`
- **AND** SHALL set `hdxQuery.Interval = q.Interval`
- **AND** SHALL set `hdxQuery.Headers` from the headers stashed on `ctx` by the package's request-scoped mutator
- **AND** SHALL call `ds.Register(hdxQueryKey{}, hdxQuery)`

#### Scenario: PreInterpolate handles unparseable JSON

- **WHEN** `raw` is not valid JSON for the `HDXQuery` model
- **THEN** `PreInterpolate` SHALL return a downstream-classified error (`backend.DownstreamError`)
- **AND** no macro SHALL fire after the error

### Requirement: `MetaDataProvider` SHALL access plugin services via the registry

`MetaDataProvider` SHALL hold a reference to its parent `*sqlds.SQLDatasource` for issuing `QueryData` calls. It SHALL resolve `DataSourceInstanceSettings` via `ds.Resolve(instanceSettingsKey{})` rather than via a Connector-level accessor. PK and ad-hoc-key results SHALL continue to be cached with the same TTL semantics as the fork (`time.Hour` TTL via `ttlcache/v3`).

#### Scenario: MetaDataProvider resolves default database from settings

- **WHEN** `MetaDataProvider.GetPK` is called with an empty `database` argument
- **THEN** it SHALL call `ds.Resolve(instanceSettingsKey{})` to obtain `DataSourceInstanceSettings`
- **AND** SHALL parse `PluginSettings` from those settings via `hdx/models.NewPluginSettings`
- **AND** SHALL use `PluginSettings.DefaultDatabase` as the database name

#### Scenario: Cache hit returns cached PK without QueryData call

- **WHEN** `MetaDataProvider.GetPK` is called and the `(database, table)` key is in the PK cache and not expired
- **THEN** it SHALL return the cached value
- **AND** SHALL NOT call `ds.QueryData`

### Requirement: HDX interpolator SHALL implement upstream `sqlds.Interpolator`

The new package's interpolator SHALL satisfy upstream's `sqlds.Interpolator` interface (`Interpolate(ctx context.Context, ds *sqlds.SQLDatasource, q *sqlutil.Query, raw json.RawMessage) (string, error)`). The implementation SHALL use ClickHouse AST parsing to determine macro positions, fall back to regex-only matching when AST parsing fails, and process macros in reverse position order to keep byte offsets stable.

#### Scenario: AST parse succeeds — macros expand at AST-derived positions

- **WHEN** the input SQL parses cleanly via `clickhouse-sql-parser`
- **THEN** the interpolator SHALL collect AST positions for each macro identifier
- **AND** SHALL invoke each macro with its AST-derived `parser.Pos`

#### Scenario: AST parse fails — interpolator falls back to regex-only

- **WHEN** the input SQL cannot be AST-parsed (e.g. unsupported syntax)
- **THEN** the interpolator SHALL match macros via regex only
- **AND** SHALL still invoke each macro, passing the byte-offset position

#### Scenario: Escaped macro form passes through unchanged

- **WHEN** the SQL contains `$$__macroName(...)` (double-dollar escape)
- **THEN** the interpolator SHALL emit `$__macroName(...)` (one dollar stripped)
- **AND** SHALL NOT invoke the macro

### Requirement: `AdHocFilterMacro` SHALL emit safely-escaped SQL literals

The port of `AdHocFilterMacro` into the new package SHALL emit single-quoted SQL string literals with `'` escaped as `\'` and `\` escaped as `\\`. Dollar-quoted literals (`$value$`) SHALL NOT be used. An internal helper `escapeSQLLiteral(s string) string` SHALL be the single point of escape application. Tests SHALL cover injection vectors including embedded single quotes, backslashes, and combinations.

#### Scenario: Ad-hoc filter value containing a single quote

- **WHEN** an ad-hoc filter with value `O'Reilly` is interpolated
- **THEN** the emitted condition SHALL include `'O\'Reilly'`
- **AND** SHALL NOT include `$O'Reilly$`

#### Scenario: Ad-hoc filter value containing a backslash

- **WHEN** an ad-hoc filter with value `path\to\thing` is interpolated
- **THEN** the emitted condition SHALL include `'path\\to\\thing'`

#### Scenario: Combined injection attempt

- **WHEN** an ad-hoc filter with value `' OR 1=1 --` is interpolated
- **THEN** the emitted condition SHALL include `'\' OR 1=1 --'`
- **AND** the resulting SQL SHALL parse as a single string literal followed by the surrounding macro context, not as injected SQL
