// Package hdx is a proof-of-concept demonstration that the Hydrolix-flavoured
// behaviour previously bolted into the hydrolix/sqlds fork can be hosted on
// top of upstream grafana/sqlds purely via its extension points: the service
// registry (Register/Resolve), context-aware macros (RegisterMacro), the
// pre/post interpolation hooks, and the pluggable Interpolator interface.
//
// Migration entry point for plugins moving off the fork:
//
//	conn, err := sqlds.NewConnector(ctx, NewHydrolix(), settings, false)
//	if err != nil { return nil, err }
//	ds := hdx.NewDatasource(NewHydrolix())   // returns *sqlds.SQLDatasource
//	ds.CustomRoutes = api.Routes(ds)         // pre-existing plugin routes
//	return ds.NewDatasource(ctx, settings)
//
// All HDX macros (`$__adHocFilter`, `$__timeFilter` and siblings) are
// registered on the returned datasource; HDXQuery parsing happens once per
// query in the PreInterpolate hook; the MetaDataProvider is reachable from
// any macro via mctx.Resolve(metadataProviderKey{}). If the supplied driver
// implements hdx.InterpolatedQueryMutator, PostInterpolate is wired through.
//
// Constraint of the PoC: no file outside ./hdx is modified, and the root
// go.mod gains only the two HDX-specific dependencies. The production
// package would live at github.com/hydrolix/sqlds-hdx with its own go.mod.
package hdx

import (
	"context"
	"encoding/json"

	"github.com/grafana/grafana-plugin-sdk-go/backend"

	"github.com/grafana/sqlds/v5"
)

// NewDatasource builds an upstream *sqlds.SQLDatasource pre-wired with HDX
// behaviour. The returned value is a stock *sqlds.SQLDatasource — plugins
// continue to call its second-stage NewDatasource(ctx, settings) method, set
// CustomRoutes, etc.
func NewDatasource(driver sqlds.Driver) *sqlds.SQLDatasource {
	w := &wrapperDriver{Driver: driver}
	ds := sqlds.NewDatasource(w)
	w.ds = ds

	ds.PreInterpolate = preInterpolate
	if mutator, ok := driver.(InterpolatedQueryMutator); ok {
		ds.PostInterpolate = bridgeInterpolatedQueryMutator(mutator)
	}

	ds.Register(metadataProviderKey{}, NewMetaDataProvider(ds))

	for name, fn := range Macros {
		ds.RegisterMacro(name, shim(fn))
	}

	return ds
}

// shim adapts an HDX-flavoured macro to the upstream ContextMacroFunc
// signature. The shim resolves *HDXQuery (populated by PreInterpolate) and
// *MetaDataProvider (registered by NewDatasource) from the datasource
// service registry on every invocation. Resolution failures surface as
// macro errors rather than panics.
func shim(fn HDXMacroFunc) sqlds.ContextMacroFunc {
	return func(mctx sqlds.MacroContext, args []string) (string, error) {
		rawQuery, ok := mctx.Resolve(hdxQueryKey{})
		if !ok {
			return "", missingRegistration("HDXQuery", "PreInterpolate did not populate hdxQueryKey{}")
		}
		query, ok := rawQuery.(*HDXQuery)
		if !ok {
			return "", missingRegistration("HDXQuery", "registry held wrong type")
		}

		var mdp *MetaDataProvider
		if rawMdp, ok := mctx.Resolve(metadataProviderKey{}); ok {
			mdp, _ = rawMdp.(*MetaDataProvider)
		}

		// query.WithSQL surfaces the in-flight SQL — at this point in upstream
		// the macros run on the pre-rewrite source SQL because the
		// DefaultInterpolator's context-macro pass expands from highest
		// position to lowest in a single pass, so prior-position rewrites
		// have not happened yet. Using mctx.Query().RawSQL keeps macros that
		// re-parse the SQL (AdHocFilterMacro via GetMacroCTEs, getPK) aligned
		// with the position they were invoked at.
		q := query.WithSQL(mctx.Query().RawSQL)
		return fn(mctx.Context(), q, args, parserPos(mctx.Pos()), mdp)
	}
}

// preInterpolate is the PreInterpolate hook installed by NewDatasource. It
// parses the raw DataQuery.JSON into an *HDXQuery, copies the time range
// and interval off the upstream *Query, attaches headers stashed on
// context by wrapperDriver.MutateQueryData, and registers the result on the
// datasource so the shim can read it from each macro invocation.
//
// PreInterpolate has no direct *SQLDatasource handle; the upstream
// interpolator calls it with (ctx, query, rawJSON). The hook recovers
// the datasource via the unexported access path on the ds — but since we
// can't import private state, we instead encode the datasource on the
// context inside wrapperDriver.MutateQueryData before queries dispatch.
func preInterpolate(ctx context.Context, q *sqlds.Query, raw json.RawMessage) error {
	hq := &HDXQuery{}
	if err := json.Unmarshal(raw, hq); err != nil {
		return backend.DownstreamError(err)
	}
	hq.TimeRange = q.TimeRange
	hq.Interval = q.Interval
	if h := headersFromContext(ctx); h != nil {
		hq.Headers = h
	}
	ds := datasourceFromContext(ctx)
	if ds == nil {
		// Direct interpolate path (e.g. plugin's /interpolate resource route)
		// — caller is expected to inject the datasource onto the context via
		// hdx.WithDatasource(ctx, ds). Skipping registration here would
		// silently break ad-hoc-filter macros downstream, so surface the
		// misuse as an error.
		return backend.DownstreamError(errNoDatasourceOnContext)
	}
	ds.Register(hdxQueryKey{}, hq)
	return nil
}

// bridgeInterpolatedQueryMutator wires a driver-level
// InterpolatedQueryMutator into upstream's PostInterpolate hook on
// *SQLDatasource. The returned ctx from the driver is currently discarded
// because PostInterpolate has no context-return slot — drivers that need
// context derivation should migrate to QueryDataMutator instead.
func bridgeInterpolatedQueryMutator(m InterpolatedQueryMutator) func(ctx context.Context, q *sqlds.Query, sql string) (string, error) {
	return func(ctx context.Context, _ *sqlds.Query, sql string) (string, error) {
		_, mutated := m.MutateInterpolatedQuery(ctx, sql)
		return mutated, nil
	}
}
