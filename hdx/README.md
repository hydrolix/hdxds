Implementation complete — PoC in ./hdx

Both user corrections applied: hydrolix/clickhouse-sql-parser v0.5.1 (bumped, no AfterShip transitive — gone from go.mod/go.sum). Parser API differences in v0.5.1 (Ident.Name field instead of String(), formatter-based stringification) were handled in hdx/parsing.go with a new formatExpr helper.

Files created (all under ./hdx/, 1512 lines total)

┌────────────────────────┬────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────┐
│          File          │                                                                                              What it does                                                                                              │
├────────────────────────┼────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────┤
│ hdx/keys.go            │ Registry key types + InterpolatedQueryMutator driver interface + compile-time assertion that upstream Interpolator shape is what we expect                                                             │
├────────────────────────┼────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────┤
│ hdx/models/settings.go │ PluginSettings verbatim from fork                                                                                                                                                                      │
├────────────────────────┼────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────┤
│ hdx/query.go           │ HDXQuery, AdHocFilter, WithSQL, GetHdxQuery                                                                                                                                                            │
├────────────────────────┼────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────┤
│ hdx/parsing.go         │ AST visitors, GetMacroCTEs, RoundTimeRange, formatExpr helper (new — replaces fork's removed .String())                                                                                                │
├────────────────────────┼────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────┤
│ hdx/macros.go          │ All 14 HDX macros with SECURITY: header + escapeSQLLiteral helper. Every user-value interpolation in buildFilterCondition/buildArrayCondition uses quoteLiteral; zero $…$ dollar-quoted output remains │
├────────────────────────┼────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────┤
│ hdx/metadata.go        │ MetaDataProvider with TTL caches; resolves instance settings via ds.Resolve(instanceSettingsKey{}) — no Connector accessor needed                                                                      │
├────────────────────────┼────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────┤
│ hdx/datasource.go      │ NewDatasource(driver) bootstrap + shim + preInterpolate + bridgeInterpolatedQueryMutator                                                                                                               │
├────────────────────────┼────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────┤
│ hdx/wiring.go          │ wrapperDriver (intercepts MutateQueryData to stash datasource handle + headers + settings); WithDatasource helper for direct-interpolation paths                                                       │
├────────────────────────┼────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────┤
│ hdx/hdx_test.go        │ 9 tests covering all extension-point wiring and three injection vectors                                                                                                                                │
└────────────────────────┴────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────┘

All four extension points exercised

1. Service registry — ds.Register(metadataProviderKey{}, NewMetaDataProvider(ds)); mctx.Resolve(...) inside shim
2. RegisterMacro / ContextMacroFunc — 14 HDX macros registered via shim(HDXMacroFunc) sqlds.ContextMacroFunc
3. PreInterpolate — parses raw JSON into *HDXQuery, attaches TimeRange/Interval/Headers, registers under hdxQueryKey{}
4. PostInterpolate — bridges driver's InterpolatedQueryMutator only when the driver implements it; nil otherwise

Validation

- go build ./... clean
- go vet ./... clean
- go test ./... — all packages pass (sqlds 5.2s, sqlds/hdx 1.1s)
- openspec validate migrate-hdx-to-external --strict valid
- git status: only go.mod, go.sum modified; hdx/ and openspec/changes/migrate-hdx-to-external/ new — no file outside ./hdx touched
- OpenSpec progress: 31/39 (remaining 8 are tasks 7.x/8.x, out of scope for the PoC)

Try it

ds := hdx.NewDatasource(yourDriver)   // returns *sqlds.SQLDatasource
ds.CustomRoutes = api.Routes(ds)       // existing plugin route map
return ds.NewDatasource(ctx, settings)