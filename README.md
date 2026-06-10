[![Build Status](https://drone.grafana.net/api/badges/grafana/sqlds/status.svg)](https://drone.grafana.net/grafana/sqlds)

# sqlds

`sqlds` stands for `SQL Datasource`.

Most SQL-driven datasources, like `Postgres`, `MySQL`, and `MSSQL` share extremely similar codebases.

The `sqlds` package is intended to remove the repetition of these datasources and centralize the datasource logic. The only thing that the datasources themselves should have to define is connecting to the database, and what driver to use, and the plugin frontend.

**Usage**

```go
if err := datasource.Manage("my-datasource", datasourceFactory, datasource.ManageOpts{}); err != nil {
  log.DefaultLogger.Error(err.Error())
  os.Exit(1)
}

func datasourceFactory(ctx context.Context, s backend.DataSourceInstanceSettings) (instancemgmt.Instance, error) {
  ds := sqlds.NewDatasource(&myDatasource{})
  return ds.NewDatasource(ctx, s)
}
```

## Standardization

### Macros

The `sqlds` package formerly defined a set of default macros, but those have been migrated to `grafana-plugin-sdk-go`: see [the code](https://github.com/grafana/grafana-plugin-sdk-go/blob/main/data/sqlutil/macros.go) for details.

## Extension points

Datasource plugins that need behaviour beyond what `sqlds` provides — backend-specific macros that depend on metadata round-trips, AST-aware interpolation, per-query setup, or post-rewrite mutation — can supply it from their own package instead of forking `sqlds`. Four extension surfaces are available; using any of them is opt-in and leaves the legacy code path unchanged.

### Service registry

`SQLDatasource.Register(key, value)` and `Resolve(key) (any, bool)` attach
plugin-level singletons (metadata providers, parsers, caches) to a
datasource. Use an unexported package-local type as the key to avoid
collisions across plugins.

```go
type metadataProviderKey struct{}
ds.Register(metadataProviderKey{}, newMetadataProvider(ds))
```

### Context-aware macros

`SQLDatasource.RegisterMacro(name, fn ContextMacroFunc)` registers a macro
that receives a `MacroContext` carrying the request `context.Context`, the
parsed query, the raw `DataQuery.JSON` (for plugins that want to unmarshal
their own query model), the macro's byte offset in the source SQL, and a
`Resolve` accessor that proxies to the service registry.

```go
ds.RegisterMacro("adHocFilter", func(mctx sqlds.MacroContext, args []string) (string, error) {
    v, _ := mctx.Resolve(metadataProviderKey{})
    provider := v.(*MetadataProvider)
    // ...
    return condition, nil
})
```

The legacy `sqlutil.MacroFunc` registration via the driver's `Macros()`
method continues to work. A single query may reference both kinds.

**Value escaping is the macro's responsibility.** `sqlds` does not provide
a parameter binder. Macros that interpolate user-supplied values must
emit safely-quoted SQL literals — for ClickHouse / MySQL style backends,
single-quoted strings with `'` escaped as `\'` and `\` escaped as `\\`.

```go
func escape(s string) string {
    s = strings.ReplaceAll(s, `\`, `\\`)
    s = strings.ReplaceAll(s, `'`, `\'`)
    return s
}
// inside the macro:
return fmt.Sprintf("col = '%s'", escape(value)), nil
```

### Pluggable interpolator

`SQLDatasource.Interpolator` accepts any implementation of the
`Interpolator` interface. The default behaves byte-for-byte like the
legacy `sqlutil.Interpolate` path when no context macros, services, or
hooks are configured.

### Pre / post hooks

`SQLDatasource.PreInterpolate(ctx, *Query, json.RawMessage) error` fires
once per query before any macro expansion — useful for per-query setup
such as parsing the SQL into an AST once and stashing it on the service
registry. `SQLDatasource.PostInterpolate(ctx, *Query, sql string) (string,
error)` fires once on the rewritten SQL — useful for final-pass
adjustments such as injecting `LIMIT` clauses or rewriting projections.
Hooks are single-valued; compose chains in your plugin if you need
multiple.

### Migrating off a fork

If you currently maintain a fork of `sqlds` to add custom macros, an
AST-aware interpolator, or a post-rewrite hook, those additions can
typically move into a small external package that imports `sqlds` and
registers behaviour through the surfaces above. Filing an issue with
your concrete use case is encouraged so we can confirm fit before you
invest in the migration.
