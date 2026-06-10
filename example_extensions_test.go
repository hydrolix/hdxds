package sqlds_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/grafana/grafana-plugin-sdk-go/data/sqlutil"
	"github.com/grafana/sqlds/v5"
)

// ExampleSQLDatasource_Register shows the unexported-key idiom for attaching
// a plugin-level service to a datasource. Using an unexported type as the
// key prevents collisions across plugins, mirroring the context.Context
// value-key convention.
func ExampleSQLDatasource_Register() {
	type metadataProviderKey struct{}

	var ds sqlds.SQLDatasource
	ds.Register(metadataProviderKey{}, "the-provider")

	v, _ := ds.Resolve(metadataProviderKey{})
	fmt.Println(v)
	// Output: the-provider
}

// ExampleSQLDatasource_RegisterMacro shows a context-aware macro that
// reaches a registered service and emits a safely-escaped single-quoted
// string literal. Value escaping is the macro's responsibility — sqlds
// does not provide a parameter binder.
func ExampleSQLDatasource_RegisterMacro() {
	type prefixKey struct{}

	var ds sqlds.SQLDatasource
	ds.Register(prefixKey{}, "prod")

	ds.RegisterMacro("envFilter", func(mctx sqlds.MacroContext, args []string) (string, error) {
		v, _ := mctx.Resolve(prefixKey{})
		prefix := v.(string)
		// Escape both backslash and single quote to keep '...' safe.
		escape := func(s string) string {
			s = strings.ReplaceAll(s, `\`, `\\`)
			s = strings.ReplaceAll(s, `'`, `\'`)
			return s
		}
		return fmt.Sprintf("env = '%s'", escape(prefix)), nil
	})
	fmt.Println("registered envFilter macro")
	// Output: registered envFilter macro
}

// ExampleSQLDatasource_PostInterpolate shows the post-interpolation hook
// rewriting the final SQL — here, appending a row-limit clause.
func ExampleSQLDatasource_PostInterpolate() {
	var ds sqlds.SQLDatasource
	ds.PostInterpolate = func(ctx context.Context, q *sqlutil.Query, sql string) (string, error) {
		return sql + " LIMIT 1000", nil
	}
	// In production the interpolator runs inside QueryData; here we
	// simulate the hook to show its shape.
	out, _ := ds.PostInterpolate(context.Background(), &sqlutil.Query{}, "SELECT 1")
	fmt.Println(out)
	// Output: SELECT 1 LIMIT 1000
}

// ExampleSQLDatasource_PreInterpolate shows the pre-interpolation hook
// doing per-query setup — here, parsing the raw query JSON once and
// stashing a derived value on the service registry so subsequent macros
// can read it without re-parsing.
func ExampleSQLDatasource_PreInterpolate() {
	type parsedKey struct{}
	var ds sqlds.SQLDatasource

	ds.PreInterpolate = func(ctx context.Context, q *sqlutil.Query, raw json.RawMessage) error {
		var model struct {
			Tag string `json:"tag"`
		}
		_ = json.Unmarshal(raw, &model)
		ds.Register(parsedKey{}, model.Tag)
		return nil
	}

	_ = ds.PreInterpolate(context.Background(), &sqlutil.Query{}, []byte(`{"tag":"hello"}`))
	v, _ := ds.Resolve(parsedKey{})
	fmt.Println(v)
	// Output: hello
}
