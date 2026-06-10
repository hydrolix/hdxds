package sqlds

import (
	"context"
	"encoding/json"

	"github.com/grafana/grafana-plugin-sdk-go/data/sqlutil"
)

// MacroContext is supplied to a ContextMacroFunc each time the macro is
// invoked. It exposes the request-scoped context, the parsed query, the raw
// DataQuery JSON (so plugins can unmarshal their own query model without
// sqlds knowing the shape), the macro's byte offset in the source SQL, and
// access to services attached to the datasource.
//
// A MacroContext value is valid only for the duration of a single
// interpolation call. Holding onto it past the return of the ContextMacroFunc
// is undefined.
//
// Value escaping is the macro's responsibility. sqlds does not provide a
// parameter binder. A macro that interpolates a user-supplied value MUST
// emit a safely-quoted SQL literal. The canonical pattern for ClickHouse /
// MySQL style backends is:
//
//	func escape(s string) string {
//	    s = strings.ReplaceAll(s, `\`, `\\`)
//	    s = strings.ReplaceAll(s, `'`, `\'`)
//	    return s
//	}
//	// inside the macro:
//	return fmt.Sprintf("col = '%s'", escape(value)), nil
type MacroContext interface {
	// Context returns the request-scoped context.
	Context() context.Context
	// Query returns the parsed sqlutil.Query as sqlds constructs it. The
	// RawSQL field is the original source SQL, not any partially-rewritten
	// form, so Pos() values index into Query().RawSQL.
	Query() *sqlutil.Query
	// QueryJSON returns the raw backend.DataQuery.JSON for this query.
	// Plugins use it to unmarshal their own query model when the standard
	// sqlutil.Query shape is not enough (e.g. extra filter fields).
	QueryJSON() json.RawMessage
	// Pos is the byte offset of this macro invocation in Query().RawSQL.
	Pos() int
	// Resolve looks up a service previously attached to the datasource via
	// SQLDatasource.Register. Returns (nil, false) if no value is registered
	// for the key.
	Resolve(key any) (any, bool)
}

// ContextMacroFunc is the new-style macro signature that receives a
// MacroContext. Register implementations with SQLDatasource.RegisterMacro.
// The legacy sqlutil.MacroFunc continues to work; the two coexist and a
// single query may reference both kinds.
type ContextMacroFunc func(mctx MacroContext, args []string) (string, error)

// macroContext is the unexported MacroContext implementation constructed by
// the interpolator per macro invocation.
type macroContext struct {
	ctx     context.Context
	ds      *SQLDatasource
	query   *sqlutil.Query
	rawJSON json.RawMessage
	pos     int
}

func (m *macroContext) Context() context.Context    { return m.ctx }
func (m *macroContext) Query() *sqlutil.Query       { return m.query }
func (m *macroContext) QueryJSON() json.RawMessage  { return m.rawJSON }
func (m *macroContext) Pos() int                    { return m.pos }
func (m *macroContext) Resolve(key any) (any, bool) { return m.ds.Resolve(key) }

// Register attaches a service to the datasource. Services live for the
// lifetime of the SQLDatasource instance and are reachable from custom
// macros via MacroContext.Resolve.
//
// Keys are compared by Go equality (==). To avoid accidental collisions
// across plugins, use an unexported package-local type as the key — the
// same idiom as context.Context value keys:
//
//	type metadataProviderKey struct{}
//	ds.Register(metadataProviderKey{}, provider)
//	// later, from inside a macro:
//	v, _ := mctx.Resolve(metadataProviderKey{})
//	provider := v.(*MetadataProvider)
//
// Calling Register with a key that has already been registered overwrites
// the previous value. Register is safe for concurrent use.
func (ds *SQLDatasource) Register(key any, value any) {
	ds.services.Store(key, value)
}

// Resolve returns the service previously attached via Register. Returns
// (nil, false) if no service is registered for the key. Resolve is safe
// for concurrent use.
func (ds *SQLDatasource) Resolve(key any) (any, bool) {
	return ds.services.Load(key)
}

// RegisterMacro registers a new-style macro under the given name. The name
// must match the bareword after $__ in source SQL (e.g. "myMacro" matches
// $__myMacro(...)). Registering a name that is already registered
// overwrites the previous registration.
func (ds *SQLDatasource) RegisterMacro(name string, fn ContextMacroFunc) {
	ds.contextMacrosMu.Lock()
	defer ds.contextMacrosMu.Unlock()
	if ds.contextMacros == nil {
		ds.contextMacros = make(map[string]ContextMacroFunc)
	}
	ds.contextMacros[name] = fn
}

// contextMacrosSnapshot returns a copy of the registered context macros so
// the interpolator can iterate without holding the lock.
func (ds *SQLDatasource) contextMacrosSnapshot() map[string]ContextMacroFunc {
	ds.contextMacrosMu.RLock()
	defer ds.contextMacrosMu.RUnlock()
	if len(ds.contextMacros) == 0 {
		return nil
	}
	out := make(map[string]ContextMacroFunc, len(ds.contextMacros))
	for k, v := range ds.contextMacros {
		out[k] = v
	}
	return out
}
