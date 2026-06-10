## ADDED Requirements

### Requirement: `Interpolator` interface with default implementation

`sqlds` SHALL expose an `Interpolator` interface and a default implementation that wraps `sqlutil.Interpolate`.

```go
type Interpolator interface {
    Interpolate(ctx context.Context, ds *SQLDatasource, query *sqlutil.Query, rawJSON json.RawMessage) (sql string, err error)
}
```

A `DefaultInterpolator` SHALL be provided that:
- invokes registered macros (both `sqlutil.MacroFunc` and the new `MacroFunc`);
- supplies a `MacroContext` to new-style macros;
- runs the `PreInterpolate` hook before any macro fires (if set);
- runs the `PostInterpolate` hook after macro expansion (if set);
- returns the rewritten SQL as `sql` and any errors during macro expansion or hook execution.

#### Scenario: Default interpolator preserves legacy behavior

- **WHEN** a datasource has no custom `Interpolator`, no hooks, no services, no new-style macros
- **AND** a query is executed
- **THEN** the SQL passed to the driver is identical to the SQL `sqlutil.Interpolate` would produce for the same input

### Requirement: Per-datasource interpolator override

`SQLDatasource` SHALL expose a public `Interpolator` field. If non-nil, this implementation replaces the default for all queries on that datasource.

#### Scenario: A plugin installs an AST-based interpolator

- **WHEN** a plugin sets `ds.Interpolator = myAstInterpolator` at datasource construction
- **AND** a query is executed
- **THEN** `myAstInterpolator.Interpolate` is invoked and `DefaultInterpolator` is not

#### Scenario: Nil falls back to default

- **WHEN** `ds.Interpolator` is `nil`
- **AND** a query is executed
- **THEN** `DefaultInterpolator` is invoked

### Requirement: `PreInterpolate` and `PostInterpolate` hooks

`SQLDatasource` SHALL expose two public hook fields:

```go
PreInterpolate  func(ctx context.Context, query *sqlutil.Query, rawJSON json.RawMessage) error
PostInterpolate func(ctx context.Context, query *sqlutil.Query, sql string) (string, error)
```

`PreInterpolate`, if non-nil, SHALL be invoked exactly once per query before any macro expansion. Returning a non-nil error SHALL abort interpolation and propagate the error to the caller.

`PostInterpolate`, if non-nil, SHALL be invoked exactly once per query after all macros have expanded, receiving the rewritten SQL. The returned string SHALL replace the rewritten SQL. Returning a non-nil error SHALL propagate to the caller.

#### Scenario: `PreInterpolate` runs once per query, before macros

- **WHEN** a query references three macros and `PreInterpolate` is set
- **AND** the query is executed
- **THEN** `PreInterpolate` is invoked exactly once before any macro is invoked

#### Scenario: `PostInterpolate` rewrites the final SQL

- **WHEN** macros expand to `SELECT * FROM t WHERE a = 1`
- **AND** `PostInterpolate` returns `"SELECT * FROM t WHERE a = 1 LIMIT 1000"`
- **THEN** the SQL sent to the driver is `SELECT * FROM t WHERE a = 1 LIMIT 1000`

#### Scenario: `PreInterpolate` error aborts the query

- **WHEN** `PreInterpolate` returns an error
- **THEN** macros are not invoked, the driver is not called, and the error propagates to the caller

#### Scenario: Hooks left unset behave as no-ops

- **WHEN** both `PreInterpolate` and `PostInterpolate` are nil
- **AND** a query is executed
- **THEN** interpolation proceeds identically to the case where the fields did not exist
