> **PoC scope.** Implementation lives under `./hdx` in this repository using the root `go.mod` (import path `github.com/grafana/sqlds/v5/hdx` — this branch is upstream-aligned, module is `github.com/grafana/sqlds/v5`). The PoC's purpose is to demonstrate that the four extension points from [[add-extension-points]] are sufficient to host HDX behaviour — **no new features are added to the upstream `sqlds` API and no file outside `./hdx` is modified** (root `go.mod`/`go.sum` excepted, since the HDX dependencies must be declared somewhere). Production retirement of the fork — separate repo, plugin migration, deprecation, archival — remains future work under [[hdx-fork-retirement]] and is **out of scope for the PoC**. See design.md Decision 1 / 1.5.
>
> Task groups 7 (plugin migration) and 8 (fork retirement) are retained in the spec but removed from active task tracking: they target other repos and only run after the PoC validates the approach.

## 1. Scaffolding in `./hdx`

- [x] 1.1 Create `./hdx/` directory at the repo root
- [x] 1.2 Add unexported registry-key types in `hdx/keys.go`: `hdxQueryKey{}`, `metadataProviderKey{}`, `headersCtxKey{}`, `instanceSettingsKey{}`
- [x] 1.3 Define driver-side `InterpolatedQueryMutator` interface in `hdx/driver.go`
- [x] 1.4 Add HDX deps to root `go.mod`: `github.com/hydrolix/clickhouse-sql-parser`, `github.com/jellydator/ttlcache/v3`. Note in design.md that these leave the root `go.mod` when the PoC graduates to a separate module.

## 2. Port HDX symbols to `./hdx`

- [x] 2.1 Port `macros.go` → `hdx/macros.go`; package `hdx`; rename `MacroFunc` → `HDXMacroFunc`
- [x] 2.2 Port `metadata.go` → `hdx/metadata.go`; replace `*HydrolixDatasource` field with `*sqlds.SQLDatasource`; resolve `DataSourceInstanceSettings` via `ds.Resolve(instanceSettingsKey{})` instead of `Connector.getInstanceSettings()`
- [x] 2.3 Port `interpolator.go` → `hdx/interpolator.go`; keep AST visitors, `MacroId`, `CTE`, `GetMacroCTEs`, `RoundTimeRange`, `HDXQuery`, `AdHocFilter`, `GetHdxQuery`, `WithSQL`
- [x] 2.4 Port `models/settings.go` → `hdx/models/settings.go`; keep `PluginSettings`, `QuerySetting`, `NewPluginSettings`, validators

## 3. Fix `AdHocFilterMacro` escaping at port time

- [x] 3.1 Add internal `escapeSQLLiteral(s string) string` that replaces `\` with `\\` then `'` with `\'`
- [x] 3.2 Rewrite `buildFilterCondition`, `buildArrayCondition`, and any other macro that interpolates user-supplied strings to wrap values in `'…'` with `escapeSQLLiteral` applied
- [x] 3.3 Delete every use of `$…$` dollar-quoted literal interpolation from `hdx/macros.go`
- [x] 3.4 Add at least one injection-vector test (`O'Reilly`, `' OR 1=1 --`)
- [x] 3.5 Add a top-of-file `// SECURITY:` comment in `hdx/macros.go` referencing the original incident

## 4. Implement the extension-point shim layer

- [x] 4.1 Implement `shim(fn HDXMacroFunc) sqlds.ContextMacroFunc` resolving `*HDXQuery` and `*MetaDataProvider` from the macro context's registry
- [x] 4.2 Implement `HDXInterpolator` satisfying upstream `sqlds.Interpolator` interface; lift the AST-based body
- [x] 4.3 Implement `preInterpolate(ctx, q, raw)` populating `*HDXQuery` from raw JSON + `q.TimeRange` + `q.Interval` + context-stashed headers, then `ds.Register(hdxQueryKey{}, hdxQuery)`
- [x] 4.4 Implement headers-on-context plumbing via a `QueryDataMutator` that stashes `req.Headers` on context
- [x] 4.5 If driver implements `hdx.InterpolatedQueryMutator`, wire it into upstream `ds.PostInterpolate`

## 5. Bootstrap `hdx.NewDatasource(driver)`

- [x] 5.1 Construct `ds := sqlds.NewDatasource(driver)`
- [x] 5.2 Set `ds.Interpolator = &HDXInterpolator{}`
- [x] 5.3 Set `ds.PreInterpolate = preInterpolate`
- [x] 5.4 Set `ds.PostInterpolate` from driver bridge (4.5) if applicable
- [x] 5.5 Construct `mdp := NewMetaDataProvider(ds)` and `ds.Register(metadataProviderKey{}, mdp)`
- [x] 5.6 Loop `Macros` and call `ds.RegisterMacro(name, shim(fn))` for each entry
- [x] 5.7 Document the bootstrap and its assumptions in a GoDoc comment

## 6. Smoke tests

- [x] 6.1 End-to-end smoke test: build an `hdx.NewDatasource(driver)` and exercise `Interpolate` against a SQL string mixing a legacy upstream macro and an HDX time-filter macro
- [x] 6.2 Adversarial test on `AdHocFilterMacro`: `O'Reilly` and `' OR 1=1 --` produce safely-escaped single-quoted output, no `$…$`
- [x] 6.3 Verify `MetaDataProvider` reachable from any context-aware macro via `mctx.Resolve(metadataProviderKey{})`

## 7. Plugin migration (out of scope for PoC)

- [ ] 7.1 ~~Enumerate every Hydrolix Grafana plugin currently importing the fork~~ — deferred
- [ ] 7.2 ~~Per-plugin go.mod swap, type rename, smoke-test, release~~ — deferred
- [ ] 7.3 ~~Replace `ds.RegisterRoutes(...)` with `ds.CustomRoutes = ...`~~ — deferred
- [ ] 7.4 ~~Plugin CHANGELOG + provisioning docs~~ — deferred

## 8. Fork retirement (out of scope for PoC)

- [ ] 8.1 ~~Deprecation notice on fork README~~ — deferred
- [ ] 8.2 ~~Tag final `v5.x` carrying notice~~ — deferred
- [ ] 8.3 ~~Archive `hydrolix/sqlds`~~ — deferred
- [ ] 8.4 ~~Open upstream issue marking advisory resolved~~ — deferred

## 9. Validation

- [x] 9.1 Run `openspec validate migrate-hdx-to-external --strict`; resolve any issues
- [x] 9.2 Run `go build ./...`, `go vet ./...`, `go test ./hdx/...`; all pass
- [x] 9.3 Confirm no file outside `./hdx`, `./go.mod`, `./go.sum`, and `openspec/changes/migrate-hdx-to-external/` is modified
