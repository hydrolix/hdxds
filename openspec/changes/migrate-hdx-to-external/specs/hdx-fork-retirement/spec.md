## ADDED Requirements

### Requirement: Downstream plugins SHALL switch module imports

Hydrolix Grafana plugins currently importing `github.com/hydrolix/sqlds/v5` SHALL replace that dependency with `github.com/grafana/sqlds/v5` (at or above the version that includes [[add-extension-points]]) plus `github.com/hydrolix/sqlds-hdx`. The `sqlds.NewDatasource(driver)` call SHALL be replaced by `hdx.NewDatasource(driver)`. No other plugin-side change is required to obtain HDX behaviour.

#### Scenario: Plugin's go.mod after migration

- **WHEN** a plugin completes the import switch
- **THEN** `go.mod` SHALL NOT reference `github.com/hydrolix/sqlds`
- **AND** SHALL reference both `github.com/grafana/sqlds/v5` and `github.com/hydrolix/sqlds-hdx`

#### Scenario: Plugin's datasource bootstrap after migration

- **WHEN** a plugin's `datasourceFactory` runs
- **THEN** it SHALL call `hdx.NewDatasource(driver)` and use the returned `*sqlds.SQLDatasource`
- **AND** SHALL NOT call `sqlds.NewDatasource` directly

### Requirement: HDX type references SHALL be retargeted

Plugins SHALL rename type references that previously lived in the fork's `sqlds` package to the new `hdx` package: `sqlds.HDXQuery → hdx.HDXQuery`, `sqlds.AdHocFilter → hdx.AdHocFilter`, `sqlds.MetaDataProvider → hdx.MetaDataProvider`, `sqlds.MacroFunc (HDX-flavour) → hdx.HDXMacroFunc`, `sqlds.PluginSettings → hdx/models.PluginSettings`, `sqlds.Macros → hdx.Macros`. Plugins SHALL NOT need to re-register macros — that is handled by `hdx.NewDatasource`.

#### Scenario: Plugin code referencing HDXQuery

- **WHEN** a plugin source file imports `sqlds.HDXQuery`
- **THEN** the migration SHALL replace the reference with `hdx.HDXQuery`
- **AND** the corresponding import SHALL switch from `github.com/hydrolix/sqlds/v5` to `github.com/hydrolix/sqlds-hdx`

#### Scenario: Plugin code referencing PluginSettings

- **WHEN** a plugin source file imports `sqlds.PluginSettings` from the fork's `models` package
- **THEN** the migration SHALL replace the reference with `github.com/hydrolix/sqlds-hdx/models.PluginSettings`

### Requirement: Driver hook `MutateInterpolatedQuery` SHALL continue to work without change

A driver implementing the fork's `InterpolatedQueryMutator` interface (`MutateInterpolatedQuery(ctx, sql) (ctx, sql)`) SHALL continue to work after migration. The interface SHALL be redefined in the new package (`hdx.InterpolatedQueryMutator`) with the same shape. `hdx.NewDatasource` SHALL detect drivers satisfying it and install the corresponding upstream `PostInterpolate` hook.

#### Scenario: Driver implementing MutateInterpolatedQuery

- **WHEN** a driver implements `hdx.InterpolatedQueryMutator`
- **THEN** after `hdx.NewDatasource(driver)` the upstream `PostInterpolate` hook SHALL be set
- **AND** the hook SHALL call `driver.MutateInterpolatedQuery(ctx, sql)` and return the mutated SQL string

#### Scenario: Driver previously returning a derived context from MutateInterpolatedQuery

- **WHEN** a driver's `MutateInterpolatedQuery` returns a derived `context.Context`
- **THEN** the wrapper SHALL discard the returned context (PostInterpolate has no context return slot)
- **AND** this limitation SHALL be documented in the new package's GoDoc with a recommendation to migrate context-derivation logic to `QueryDataMutator.MutateQueryData` instead

### Requirement: Route registration SHALL switch from fork's method to upstream's field

Plugins SHALL replace calls to the fork's `(ds *HydrolixDatasource).RegisterRoutes(customRoutes map)` with assignment to upstream's struct field `ds.CustomRoutes = customRoutes`, performed **before** `ds.NewDatasource(ctx, settings)`. Plugin-defined routes SHALL NOT collide with upstream's `Completable` stub routes (`/schemas`, `/tables`, `/columns`), which upstream auto-registers and which return HTTP 400 `ErrorNotImplemented` while the driver does not implement `Completable`. The unused stubs SHALL be considered harmless dead routes; the plugin SHALL NOT need to remove or shadow them.

#### Scenario: Plugin sets custom routes before NewDatasource

- **WHEN** a plugin bootstraps the datasource
- **THEN** it SHALL assign `ds.CustomRoutes = api.Routes(ds)` before calling `ds.NewDatasource(ctx, settings)`
- **AND** SHALL NOT call any `RegisterRoutes(map)` method (the method does not exist on upstream `*sqlds.SQLDatasource`)

#### Scenario: Custom route collides with a Completable stub

- **WHEN** a plugin's `CustomRoutes` contains a path matching `/schemas`, `/tables`, or `/columns`
- **THEN** `ds.NewDatasource(ctx, settings)` SHALL return a `backend.PluginError` from upstream's collision check
- **AND** the plugin SHALL rename the colliding route before retrying

#### Scenario: Completable stub is called while driver lacks the interface

- **WHEN** an HTTP request reaches `/schemas`, `/tables`, or `/columns` after migration
- **THEN** upstream SHALL respond with HTTP 400 `ErrorNotImplemented` (because `ds.Completable == nil`)
- **AND** plugin functionality SHALL be unaffected (no plugin frontend code calls those paths)

### Requirement: Fork SHALL be deprecated then archived

Once all downstream Hydrolix Grafana plugins are migrated, `github.com/hydrolix/sqlds` SHALL be deprecated via a top-of-README notice pointing at `grafana/sqlds` plus `hydrolix/sqlds-hdx`. A final `v5.x` release SHALL be tagged carrying the deprecation notice. The repository SHALL then be archived on GitHub for git-archaeology purposes.

#### Scenario: Deprecation notice precedes archival

- **WHEN** the fork is to be retired
- **THEN** a deprecation notice SHALL land on `main` and be released as a tagged version BEFORE the repository is archived
- **AND** the notice SHALL state the replacement module paths and a link to the migration documentation

#### Scenario: Plugin still on the fork after archival

- **WHEN** a plugin has not migrated by the archive date
- **THEN** that plugin SHALL continue to build (the fork remains tagged and `go.mod`-resolvable)
- **AND** the plugin owner SHALL receive no upstream security or feature backports
