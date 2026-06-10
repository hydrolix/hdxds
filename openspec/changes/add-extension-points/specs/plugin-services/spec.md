## ADDED Requirements

### Requirement: Datasource-scoped service registry

`SQLDatasource` SHALL expose `Register(key any, value any)` and `Resolve(key any) (any, bool)` methods that store and retrieve plugin-attached singletons (metadata providers, parsers, caches, …) for the lifetime of the datasource instance.

Keys SHALL be compared by Go equality (`==`), so plugins SHOULD use unexported package-local types as keys to prevent accidental collisions across plugins, mirroring the `context.Context` value idiom.

Storage SHALL be safe for concurrent access from any number of goroutines (e.g. backed by `sync.Map`).

#### Scenario: Registering and resolving a service

- **WHEN** a plugin calls `ds.Register(metadataProviderKey, provider)` during datasource construction
- **AND** later calls `ds.Resolve(metadataProviderKey)` from a macro or interpolator
- **THEN** the second call returns `(provider, true)` referring to the same instance

#### Scenario: Resolving an unregistered key

- **WHEN** a plugin calls `ds.Resolve(someKey)` where `someKey` has never been registered
- **THEN** the call returns `(nil, false)`

#### Scenario: Overwriting a previously-registered key

- **WHEN** a plugin calls `ds.Register(k, v1)` and later `ds.Register(k, v2)`
- **THEN** `ds.Resolve(k)` returns `(v2, true)`

#### Scenario: Concurrent registration and resolution

- **WHEN** multiple goroutines call `Register` and `Resolve` concurrently with different keys
- **THEN** no calls return inconsistent state and no calls panic

### Requirement: Service registry is not visible across datasource instances

Each `SQLDatasource` instance SHALL have its own registry. Values registered on one instance MUST NOT be observable from `Resolve` on a different instance.

#### Scenario: Two datasources do not share state

- **WHEN** `ds1.Register(k, v)` is called
- **AND** `ds2.Resolve(k)` is called on a different `SQLDatasource` instance
- **THEN** the second call returns `(nil, false)`
