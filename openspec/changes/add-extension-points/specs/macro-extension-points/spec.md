## ADDED Requirements

### Requirement: `MacroContext` exposes query, position, services, and parameter binding to macros

`sqlds` SHALL provide a `MacroContext` interface that a macro implementation receives at invocation time. The interface SHALL expose:

- `Context() context.Context` — the request-scoped context (e.g. for downstream HTTP calls);
- `Query() *sqlutil.Query` — the parsed query (`RawSQL`, `Format`, `RefID`, `ConnectionArgs`, …) as `sqlds` already constructs it;
- `QueryJSON() json.RawMessage` — the raw `backend.DataQuery.JSON` so plugins can unmarshal their own query model (e.g. with extra filter fields) without `sqlds` knowing the shape;
- `Pos() int` — the byte offset of this macro invocation in `Query().RawSQL`;
- `Resolve(key any) (any, bool)` — proxy to the datasource service registry (see `plugin-services`).

`MacroContext` SHALL NOT include a parameter-binding method. Macros that interpolate user-supplied values are responsible for emitting safely-escaped SQL literals (typically single-quoted strings with `'` and `\` escaped) themselves.

#### Scenario: A macro reads context, raw JSON, and a registered service

- **WHEN** a plugin registers a custom macro via the new `MacroFunc` signature
- **AND** the macro calls `mctx.Context()`, `mctx.QueryJSON()`, and `mctx.Resolve(metadataProviderKey)`
- **THEN** the values returned reflect the current request's context, the raw `DataQuery.JSON` for this query, and the service previously attached at datasource construction

#### Scenario: A macro emits an escaped SQL literal

- **WHEN** a macro receives a user-supplied value `"x'y"` and returns `fmt.Sprintf("col = '%s'", escape(value))` where `escape` replaces `'` with `\'` and `\` with `\\`
- **AND** the query is executed
- **THEN** the SQL passed to the driver contains `col = 'x\'y'` and the trailing text after the escaped quote is not parsed as SQL

#### Scenario: `Pos()` reflects the macro's location in the source SQL

- **WHEN** the source SQL contains a macro at byte offset 142
- **AND** the macro is invoked
- **THEN** `mctx.Pos()` returns 142

### Requirement: New `ContextMacroFunc` signature additive to `sqlutil.MacroFunc`

`sqlds` SHALL define a new exported function type for macros that use `MacroContext`:

```go
type ContextMacroFunc func(MacroContext, []string) (string, error)
```

The name is `ContextMacroFunc` (not `MacroFunc`) because the package already exports `MacroFunc` as a deprecated alias for `sqlutil.MacroFunc`; renaming it would be a breaking change.

This SHALL NOT replace `sqlutil.MacroFunc`. Plugins SHALL be able to register macros using either type via `SQLDatasource.RegisterMacro` (for `ContextMacroFunc`) or via the existing driver `Macros()` method (for `sqlutil.MacroFunc`); the interpolator SHALL invoke the legacy type without supplying a `MacroContext` (preserving today's behavior) and the new type with a fully-populated `MacroContext`.

#### Scenario: Legacy macros keep working

- **WHEN** a plugin registers a macro using `sqlutil.MacroFunc` exactly as before
- **AND** the datasource has no `Interpolator`, no services, no hooks, no param formatter
- **THEN** queries interpolate identically to today's behavior (same output SQL for the same inputs)

#### Scenario: Mixed registration

- **WHEN** a plugin registers `$__legacyMacro` as `sqlutil.MacroFunc` and `$__newMacro` as the new `MacroFunc`
- **AND** a single query references both
- **THEN** both fire, the new one receives a `MacroContext`, and the legacy one is invoked through the existing `sqlutil` path

### Requirement: `MacroContext` does not leak across queries

A `MacroContext` instance SHALL be valid only for the duration of a single interpolation. After the interpolator returns, `Context()`, `QueryJSON()`, and `Resolve()` results MUST NOT be used.

This is a usage contract; `sqlds` is not required to actively invalidate the value, but documentation MUST state that holding onto a `MacroContext` past the call is undefined.

#### Scenario: Documentation states scope

- **WHEN** a reader reads the GoDoc for `MacroContext`
- **THEN** the doc explicitly states that the value is scoped to a single interpolation call
