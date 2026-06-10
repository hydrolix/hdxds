package hdx

import (
	"context"

	"github.com/grafana/sqlds/v5"
)

// Registry key types. Each is an unexported empty struct so that values
// stored under it cannot collide with keys from other packages. Match the
// context.Context value-key convention.

type hdxQueryKey struct{}
type metadataProviderKey struct{}
type headersCtxKey struct{}
type instanceSettingsKey struct{}

// InterpolatedQueryMutator is the driver-side hook the fork called
// MutateInterpolatedQuery. It runs once on the fully interpolated SQL
// before it reaches the database driver. The new package detects drivers
// satisfying it and wires the call into upstream's PostInterpolate hook on
// *sqlds.SQLDatasource — no upstream API change is required.
//
// Note: PostInterpolate does not return a derived context, so the returned
// ctx is observed but discarded by the wrapper. Drivers that need
// context-derivation should use QueryDataMutator.MutateQueryData instead.
type InterpolatedQueryMutator interface {
	MutateInterpolatedQuery(ctx context.Context, sql string) (context.Context, string)
}

// Compile-time assertion that the upstream Interpolator interface shape is
// what we expect. If upstream changes shape, this fails to build before any
// test runs.
var _ sqlds.Interpolator = sqlds.DefaultInterpolator{}
