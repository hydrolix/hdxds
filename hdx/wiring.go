package hdx

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/grafana/grafana-plugin-sdk-go/backend"
	"github.com/hydrolix/clickhouse-sql-parser/parser"

	"github.com/grafana/sqlds/v5"
)

// errNoDatasourceOnContext signals that PreInterpolate was invoked without
// a datasource handle on context. wrapperDriver normally injects one via
// MutateQueryData on every QueryData call. Plugins driving the interpolator
// directly (e.g. an /interpolate resource route) MUST call WithDatasource
// on the context first.
var errNoDatasourceOnContext = errors.New("hdx: no datasource on context — install via wrapperDriver.MutateQueryData (production) or hdx.WithDatasource (manual)")

// datasourceCtxKey carries *sqlds.SQLDatasource through context.
type datasourceCtxKey struct{}

// WithDatasource attaches a datasource handle to ctx so PreInterpolate can
// find it. Used by callers that drive the interpolator directly outside of
// the QueryData entry point (the plugin's /interpolate resource route is
// the existing example).
func WithDatasource(ctx context.Context, ds *sqlds.SQLDatasource) context.Context {
	return context.WithValue(ctx, datasourceCtxKey{}, ds)
}

func datasourceFromContext(ctx context.Context) *sqlds.SQLDatasource {
	v, _ := ctx.Value(datasourceCtxKey{}).(*sqlds.SQLDatasource)
	return v
}

func headersFromContext(ctx context.Context) http.Header {
	v, _ := ctx.Value(headersCtxKey{}).(http.Header)
	return v
}

// wrapperDriver wraps the user's driver to intercept QueryData entry. It
// stashes (a) the *SQLDatasource handle on context so PreInterpolate can
// register the parsed HDXQuery, (b) the request headers on context so
// HDXQuery.Headers can be populated, and (c) the DataSourceInstanceSettings
// on the upstream service registry so MetaDataProvider can resolve the
// default database without a Connector accessor.
//
// All other Driver methods (Connect, Settings, Macros, Converters) and any
// driver hooks the user's driver implements (QueryMutator, ResponseMutator,
// etc.) are forwarded through the embedded Driver. QueryDataMutator is
// the one method we override; if the user's driver also implements it, we
// invoke their implementation after our own setup so its return values win.
type wrapperDriver struct {
	sqlds.Driver
	ds *sqlds.SQLDatasource
}

func (w *wrapperDriver) MutateQueryData(ctx context.Context, req *backend.QueryDataRequest) (context.Context, *backend.QueryDataRequest) {
	if w.ds != nil && req.PluginContext.DataSourceInstanceSettings != nil {
		w.ds.Register(instanceSettingsKey{}, *req.PluginContext.DataSourceInstanceSettings)
	}
	ctx = WithDatasource(ctx, w.ds)
	ctx = context.WithValue(ctx, headersCtxKey{}, req.GetHTTPHeaders())
	if inner, ok := w.Driver.(sqlds.QueryDataMutator); ok {
		return inner.MutateQueryData(ctx, req)
	}
	return ctx, req
}

// missingRegistration formats a uniform error for the shim when an expected
// registry value is absent.
func missingRegistration(what, why string) error {
	return fmt.Errorf("hdx: %s not found in registry (%s)", what, why)
}

// parserPos is the documented identity mapping from a byte offset in the
// source SQL to the AST parser's position type. Kept as a named helper to
// make the conversion explicit at the call site and survive a future
// change to parser.Pos's underlying type.
func parserPos(i int) parser.Pos { return parser.Pos(i) }
