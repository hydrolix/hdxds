## 1. Service registry

- [x] 1.1 Add `Register(key, value any)` and `Resolve(key any) (any, bool)` methods on `SQLDatasource` backed by `sync.Map`
- [x] 1.2 Document the unexported-key idiom in GoDoc with a code example
- [x] 1.3 Write unit tests covering register/resolve, missing key, overwrite, concurrent access, and isolation between datasource instances

## 2. `MacroContext` and new `ContextMacroFunc`

> Naming note: the new function type is `ContextMacroFunc`, not `MacroFunc`. The package already exports `MacroFunc` as a deprecated alias for `sqlutil.MacroFunc`; renaming it would be a breaking change. Design and spec updated to reflect this.

- [x] 2.1 Define the `MacroContext` interface in `extensions.go` with `Context`, `Query`, `QueryJSON`, `Pos`, `Resolve` (no `BindParam`)
- [x] 2.2 Define the new `ContextMacroFunc` type `func(MacroContext, []string) (string, error)` (parallel to `sqlutil.MacroFunc`)
- [x] 2.3 Implement an unexported `macroContext` struct that satisfies the interface and is constructed by the interpolator per macro invocation
- [x] 2.4 Document the per-call lifetime contract for `MacroContext` in GoDoc
- [x] 2.5 Document in GoDoc that value escaping is the macro's responsibility (single-quoted literals with `'` → `\'`, `\` → `\\`); include a canonical example
- [x] 2.6 Unit-test `macroContext` accessors against a fake datasource (services, JSON pass-through, position)

## 3. `Interpolator` interface and default

- [x] 3.1 Define the `Interpolator` interface in a new `interpolator.go` (signature: `Interpolate(ctx, ds, query, rawJSON) (sql, err)`)
- [x] 3.2 Implement `DefaultInterpolator` that wraps `sqlutil.Interpolate` and additionally handles new-style `ContextMacroFunc` registrations
- [x] 3.3 Wire `DefaultInterpolator` to invoke `PreInterpolate` (once, before any macro) and `PostInterpolate` (once, on the rewritten SQL)
- [x] 3.4 Compute and pass `Pos()` per macro invocation (byte offset in `RawSQL`)
- [x] 3.5 Add the public `Interpolator` field on `SQLDatasource`; resolve nil to `DefaultInterpolator` at call sites
- [x] 3.6 Unit-test default behavior parity with `sqlutil.Interpolate` (golden SQL outputs for a set of legacy fixtures)
- [x] 3.7 Unit-test pre/post hook firing order, single-invocation guarantee, and error propagation
- [x] 3.8 Unit-test custom-interpolator override (default not invoked when field is set)

## 4. Interpolation hooks

- [x] 4.1 Add `PreInterpolate func(ctx, *sqlutil.Query, json.RawMessage) error` field on `SQLDatasource`
- [x] 4.2 Add `PostInterpolate func(ctx, *sqlutil.Query, string) (string, error)` field on `SQLDatasource`
- [x] 4.3 Document that hooks are single-valued and plugins compose chains themselves if needed
- [x] 4.4 Unit-test that nil hooks behave as no-ops (output identical to today)
- [x] 4.5 Unit-test that `PreInterpolate` error aborts before any macro fires
- [x] 4.6 Unit-test that `PostInterpolate`'s returned string replaces the rewritten SQL

## 5. Backwards-compatibility guarantees

- [x] 5.1 Verify the existing `sqlutil.MacroFunc` re-export in `macros.go:16` continues to work unchanged
- [x] 5.2 Run the existing test suite (`go test ./...`) and confirm zero diffs in output SQL for legacy fixtures
- [x] 5.3 Add an integration-style test covering a datasource with mixed legacy + new-style macros in a single query

## 6. Documentation

- [x] 6.1 Update `README.md` with an "Extension points" section: service registry, new `ContextMacroFunc`, `Interpolator`, hooks
- [x] 6.2 Add GoDoc examples for each public extension point (`ExampleSQLDatasource_Register`, `ExampleSQLDatasource_RegisterMacro`, `ExampleSQLDatasource_PreInterpolate`, `ExampleSQLDatasource_PostInterpolate`)
- [x] 6.3 Add a short migration note for plugin authors who today fork `sqlds` to add behavior, pointing them at the new hooks
- [x] 6.4 Note in the README that value escaping is the macro author's responsibility and link to the canonical example

## 7. Validation

- [x] 7.1 Run `openspec validate add-extension-points --strict` and resolve any issues
- [x] 7.2 Run `go vet ./...` and `go test ./...`; both must pass
- [x] 7.3 Manual smoke test: end-to-end coverage achieved by the in-package integration test `TestMixedMacros_LegacyAndContext` and the runnable GoDoc examples in `example_extensions_test.go`, which exercise all four extension points (service registry, `ContextMacroFunc`, `Interpolator`, pre/post hooks) through the real interpolator. A sibling-module example was deemed redundant for a library this size.
