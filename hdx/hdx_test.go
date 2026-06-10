package hdx

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/grafana/grafana-plugin-sdk-go/backend"
	"github.com/grafana/grafana-plugin-sdk-go/data/sqlutil"

	"github.com/grafana/sqlds/v5"
)

// minimalDriver satisfies sqlds.Driver with the smallest possible surface.
// Connect always errors because PoC tests never round-trip through SQL —
// they exercise the interpolation pipeline directly via the public
// Interpolator interface.
type minimalDriver struct{}

func (minimalDriver) Connect(_ context.Context, _ backend.DataSourceInstanceSettings, _ json.RawMessage) (*sql.DB, error) {
	return nil, errors.New("minimalDriver: connect not implemented")
}

func (minimalDriver) Settings(_ context.Context, _ backend.DataSourceInstanceSettings) sqlds.DriverSettings {
	return sqlds.DriverSettings{}
}

func (minimalDriver) Macros() sqlds.Macros            { return sqlds.Macros{} }
func (minimalDriver) Converters() []sqlutil.Converter { return nil }

// mutatorDriver also implements InterpolatedQueryMutator to prove the
// post-interpolation bridge wires up.
type mutatorDriver struct{ minimalDriver }

func (mutatorDriver) MutateInterpolatedQuery(ctx context.Context, sql string) (context.Context, string) {
	return ctx, sql + " LIMIT 100"
}

// --- registry wiring ----------------------------------------------------

func TestNewDatasource_RegistersMetaDataProvider(t *testing.T) {
	ds := NewDatasource(minimalDriver{})
	v, ok := ds.Resolve(metadataProviderKey{})
	if !ok {
		t.Fatal("MetaDataProvider not registered on the datasource")
	}
	if _, ok := v.(*MetaDataProvider); !ok {
		t.Fatalf("metadataProviderKey held %T, want *MetaDataProvider", v)
	}
}

func TestNewDatasource_PostInterpolate_WiredOnlyWhenDriverImplementsHook(t *testing.T) {
	bare := NewDatasource(minimalDriver{})
	if bare.PostInterpolate != nil {
		t.Fatal("PostInterpolate must be nil when driver does not implement InterpolatedQueryMutator")
	}
	withHook := NewDatasource(mutatorDriver{})
	if withHook.PostInterpolate == nil {
		t.Fatal("PostInterpolate must be set when driver implements InterpolatedQueryMutator")
	}
	out, err := withHook.PostInterpolate(context.Background(), &sqlds.Query{}, "SELECT 1")
	if err != nil {
		t.Fatal(err)
	}
	if out != "SELECT 1 LIMIT 100" {
		t.Fatalf("PostInterpolate did not delegate to MutateInterpolatedQuery; got %q", out)
	}
}

// --- PreInterpolate -----------------------------------------------------

func TestPreInterpolate_PopulatesHDXQuery(t *testing.T) {
	ds := NewDatasource(minimalDriver{})
	q := &sqlds.Query{
		RawSQL:    "SELECT 1",
		TimeRange: backend.TimeRange{From: time.Unix(100, 0), To: time.Unix(200, 0)},
		Interval:  5 * time.Second,
	}
	ctx := WithDatasource(context.Background(), ds)
	raw := json.RawMessage(`{"rawSql":"SELECT 1","round":"1m"}`)

	if err := ds.PreInterpolate(ctx, q, raw); err != nil {
		t.Fatalf("PreInterpolate: %v", err)
	}

	v, ok := ds.Resolve(hdxQueryKey{})
	if !ok {
		t.Fatal("PreInterpolate did not register HDXQuery on the datasource registry")
	}
	hq, ok := v.(*HDXQuery)
	if !ok {
		t.Fatalf("hdxQueryKey held %T, want *HDXQuery", v)
	}
	if hq.TimeRange != q.TimeRange {
		t.Fatalf("TimeRange not propagated: got %+v", hq.TimeRange)
	}
	if hq.Interval != q.Interval {
		t.Fatalf("Interval not propagated: got %v, want %v", hq.Interval, q.Interval)
	}
	if hq.Round != "1m" {
		t.Fatalf("Round not parsed from JSON: got %q", hq.Round)
	}
}

func TestPreInterpolate_RejectsContextWithoutDatasource(t *testing.T) {
	err := preInterpolate(context.Background(), &sqlds.Query{}, json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("expected error when ctx lacks the datasource handle")
	}
	if !strings.Contains(err.Error(), "no datasource on context") {
		t.Fatalf("error message changed: %v", err)
	}
}

// --- end-to-end macro expansion -----------------------------------------

// TestEndToEnd_MacroExpansionAndPostHook exercises the full extension-
// point pipeline through upstream's DefaultInterpolator:
//
//   - PreInterpolate parses JSON → HDXQuery → registry
//   - DefaultInterpolator finds two registered context macros and invokes
//     the shim once for each, supplying byte-offset positions
//   - The shim resolves HDXQuery + MetaDataProvider from the registry and
//     calls the HDX macro implementations
//   - PostInterpolate (driven by mutatorDriver's InterpolatedQueryMutator)
//     appends LIMIT 100
func TestEndToEnd_MacroExpansionAndPostHook(t *testing.T) {
	ds := NewDatasource(mutatorDriver{})

	rawSQL := "SELECT $__fromTime, $__toTime"
	q := &sqlds.Query{
		RawSQL:    rawSQL,
		TimeRange: backend.TimeRange{From: time.Unix(1000, 0), To: time.Unix(2000, 0)},
	}
	rawJSON := json.RawMessage(`{"rawSql":"SELECT $__fromTime, $__toTime"}`)
	ctx := WithDatasource(context.Background(), ds)

	out, err := sqlds.DefaultInterpolator{}.Interpolate(ctx, ds, q, rawJSON)
	if err != nil {
		t.Fatalf("Interpolate: %v", err)
	}
	want := "SELECT toDateTime(1000), toDateTime(2000) LIMIT 100"
	if out != want {
		t.Fatalf("got  %q\nwant %q", out, want)
	}
}

// --- security: AdHocFilter escaping -------------------------------------

func TestAdHocFilter_EscapesSingleQuote(t *testing.T) {
	filter := AdHocFilter{Key: "name", Operator: "=", Value: "O'Reilly"}
	got, err := buildFilterCondition(filter, "String")
	if err != nil {
		t.Fatalf("buildFilterCondition: %v", err)
	}
	want := `name = 'O\'Reilly'`
	if got != want {
		t.Fatalf("got  %q\nwant %q", got, want)
	}
	if strings.Contains(got, "$") {
		t.Fatalf("port leaked a dollar-quoted literal: %q", got)
	}
}

func TestAdHocFilter_EscapesBackslash(t *testing.T) {
	filter := AdHocFilter{Key: "path", Operator: "=", Value: `c:\users\admin`}
	got, err := buildFilterCondition(filter, "String")
	if err != nil {
		t.Fatalf("buildFilterCondition: %v", err)
	}
	want := `path = 'c:\\users\\admin'`
	if got != want {
		t.Fatalf("got  %q\nwant %q", got, want)
	}
}

func TestAdHocFilter_InjectionAttempt(t *testing.T) {
	filter := AdHocFilter{Key: "user", Operator: "=", Value: "' OR 1=1 --"}
	got, err := buildFilterCondition(filter, "String")
	if err != nil {
		t.Fatalf("buildFilterCondition: %v", err)
	}
	want := `user = '\' OR 1=1 --'`
	if got != want {
		t.Fatalf("got  %q\nwant %q", got, want)
	}
	// The single quote that would have terminated the literal MUST be
	// preceded by a backslash escape; the next non-quote character must
	// still be inside the literal.
	idx := strings.Index(got, `\'`)
	if idx < 0 {
		t.Fatalf("no escaped single quote in %q", got)
	}
}

func TestAdHocFilter_ArrayValues_Escape(t *testing.T) {
	filter := AdHocFilter{Key: "tags", Operator: "=|", Values: []string{"o'reilly", `a\b`}}
	got, err := buildArrayCondition(filter)
	if err != nil {
		t.Fatalf("buildArrayCondition: %v", err)
	}
	want := `(has(tags, 'o\'reilly') OR has(tags, 'a\\b'))`
	if got != want {
		t.Fatalf("got  %q\nwant %q", got, want)
	}
}

// --- escapeSQLLiteral unit tests ----------------------------------------

func TestEscapeSQLLiteral(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{``, ``},
		{`abc`, `abc`},
		{`o'reilly`, `o\'reilly`},
		{`c:\users`, `c:\\users`},
		{`'\'`, `\'\\\'`},
	}
	for _, c := range cases {
		got := escapeSQLLiteral(c.in)
		if got != c.want {
			t.Fatalf("escapeSQLLiteral(%q) = %q want %q", c.in, got, c.want)
		}
	}
}
