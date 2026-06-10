package sqlds

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/grafana/grafana-plugin-sdk-go/data/sqlutil"
)

// macroDriver is a minimal Driver used to inject legacy macros into tests.
type macroDriver struct {
	SQLMock
	macros sqlutil.Macros
}

func (d *macroDriver) Macros() Macros { return d.macros }

func newDS(legacy sqlutil.Macros) *SQLDatasource {
	if legacy == nil {
		legacy = sqlutil.Macros{}
	}
	return &SQLDatasource{
		connector: &Connector{driver: &macroDriver{macros: legacy}},
	}
}

// --- 3.6 parity with sqlutil.Interpolate -------------------------------

func TestDefaultInterpolator_LegacyParity(t *testing.T) {
	legacy := sqlutil.Macros{
		"upper": func(q *sqlutil.Query, args []string) (string, error) {
			return "UPPER(" + args[0] + ")", nil
		},
	}
	fixtures := []struct {
		sql  string
		want string
	}{
		{sql: "SELECT 1", want: "SELECT 1"},
		{sql: "SELECT $__upper(col) FROM t", want: "SELECT UPPER(col) FROM t"},
		{sql: "SELECT $__upper(col), $__upper(other) FROM t", want: "SELECT UPPER(col), UPPER(other) FROM t"},
	}
	ds := newDS(legacy)
	for _, fx := range fixtures {
		q := &sqlutil.Query{RawSQL: fx.sql}
		got, err := DefaultInterpolator{}.Interpolate(context.Background(), ds, q, nil)
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if got != fx.want {
			t.Fatalf("sql=%q\n got: %s\nwant: %s", fx.sql, got, fx.want)
		}
		// Confirm equivalence with sqlutil.Interpolate directly.
		direct, err := sqlutil.Interpolate(q, legacy)
		if err != nil {
			t.Fatalf("direct sqlutil.Interpolate err: %v", err)
		}
		if direct != got {
			t.Fatalf("parity violated for %q:\n  default: %s\n  sqlutil: %s", fx.sql, got, direct)
		}
	}
}

// --- 3.7 hooks ---------------------------------------------------------

func TestPreInterpolate_FiresBeforeMacros(t *testing.T) {
	var order []string
	legacy := sqlutil.Macros{
		"m": func(q *sqlutil.Query, _ []string) (string, error) {
			order = append(order, "macro")
			return "x", nil
		},
	}
	ds := newDS(legacy)
	ds.PreInterpolate = func(ctx context.Context, q *Query, raw json.RawMessage) error {
		order = append(order, "pre")
		return nil
	}
	q := &sqlutil.Query{RawSQL: "SELECT $__m()"}
	if _, err := ds.interpolate(context.Background(), q, nil); err != nil {
		t.Fatal(err)
	}
	if len(order) != 2 || order[0] != "pre" || order[1] != "macro" {
		t.Fatalf("order = %v want [pre macro]", order)
	}
}

func TestPostInterpolate_RewritesSQL(t *testing.T) {
	ds := newDS(nil)
	ds.PostInterpolate = func(ctx context.Context, q *Query, sql string) (string, error) {
		return sql + " LIMIT 1000", nil
	}
	q := &sqlutil.Query{RawSQL: "SELECT 1"}
	got, err := ds.interpolate(context.Background(), q, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != "SELECT 1 LIMIT 1000" {
		t.Fatalf("got %q", got)
	}
}

func TestPreInterpolate_ErrorAborts(t *testing.T) {
	macroFired := false
	legacy := sqlutil.Macros{
		"m": func(q *sqlutil.Query, _ []string) (string, error) {
			macroFired = true
			return "x", nil
		},
	}
	ds := newDS(legacy)
	wantErr := errors.New("pre boom")
	ds.PreInterpolate = func(ctx context.Context, q *Query, raw json.RawMessage) error {
		return wantErr
	}
	_, err := ds.interpolate(context.Background(), &sqlutil.Query{RawSQL: "SELECT $__m()"}, nil)
	if !errors.Is(err, wantErr) {
		t.Fatalf("got %v want %v", err, wantErr)
	}
	if macroFired {
		t.Fatal("macro should not have fired after PreInterpolate error")
	}
}

func TestPostInterpolate_ErrorPropagates(t *testing.T) {
	ds := newDS(nil)
	wantErr := errors.New("post boom")
	ds.PostInterpolate = func(ctx context.Context, q *Query, sql string) (string, error) {
		return "", wantErr
	}
	_, err := ds.interpolate(context.Background(), &sqlutil.Query{RawSQL: "SELECT 1"}, nil)
	if !errors.Is(err, wantErr) {
		t.Fatalf("got %v want %v", err, wantErr)
	}
}

func TestHooks_FireExactlyOnce(t *testing.T) {
	var pre, post int
	legacy := sqlutil.Macros{
		"m1": func(*sqlutil.Query, []string) (string, error) { return "X", nil },
		"m2": func(*sqlutil.Query, []string) (string, error) { return "Y", nil },
	}
	ds := newDS(legacy)
	ds.PreInterpolate = func(context.Context, *Query, json.RawMessage) error {
		pre++
		return nil
	}
	ds.PostInterpolate = func(_ context.Context, _ *Query, sql string) (string, error) {
		post++
		return sql, nil
	}
	q := &sqlutil.Query{RawSQL: "SELECT $__m1(), $__m2()"}
	if _, err := ds.interpolate(context.Background(), q, nil); err != nil {
		t.Fatal(err)
	}
	if pre != 1 || post != 1 {
		t.Fatalf("pre=%d post=%d want 1 each", pre, post)
	}
}

// --- 3.8 custom-interpolator override -----------------------------------

type recordingInterp struct {
	called bool
	out    string
}

func (r *recordingInterp) Interpolate(ctx context.Context, ds *SQLDatasource, q *sqlutil.Query, raw json.RawMessage) (string, error) {
	r.called = true
	return r.out, nil
}

func TestCustomInterpolator_ReplacesDefault(t *testing.T) {
	ri := &recordingInterp{out: "OVERRIDDEN"}
	ds := newDS(nil)
	ds.Interpolator = ri
	got, err := ds.interpolate(context.Background(), &sqlutil.Query{RawSQL: "SELECT $__m()"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !ri.called {
		t.Fatal("custom interpolator not called")
	}
	if got != "OVERRIDDEN" {
		t.Fatalf("got %q want OVERRIDDEN", got)
	}
}

// --- 5.3 mixed legacy + context macros ----------------------------------

func TestMixedMacros_LegacyAndContext(t *testing.T) {
	legacy := sqlutil.Macros{
		"upper": func(_ *sqlutil.Query, args []string) (string, error) {
			return "UPPER(" + args[0] + ")", nil
		},
	}
	ds := newDS(legacy)
	var seenPos int
	ds.RegisterMacro("escaped", func(mctx MacroContext, args []string) (string, error) {
		seenPos = mctx.Pos()
		// Canonical escape example: ' -> \', \ -> \\.
		v := args[0]
		return "'" + escapeLiteral(v) + "'", nil
	})
	sql := "SELECT $__upper(col), $__escaped(o'reilly) FROM t"
	q := &sqlutil.Query{RawSQL: sql}
	got, err := ds.interpolate(context.Background(), q, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := `SELECT UPPER(col), 'o\'reilly' FROM t`
	if got != want {
		t.Fatalf("got  %q\nwant %q", got, want)
	}
	// Pos for the $__escaped occurrence in the original SQL.
	wantPos := len("SELECT $__upper(col), ")
	if seenPos != wantPos {
		t.Fatalf("Pos got %d want %d", seenPos, wantPos)
	}
}

func TestContextMacro_EscapedFormStripsDollar(t *testing.T) {
	ds := newDS(nil)
	ds.RegisterMacro("m", func(mctx MacroContext, args []string) (string, error) {
		return "EXPANDED", nil
	})
	q := &sqlutil.Query{RawSQL: `SELECT $$__m(), $__m()`}
	got, err := ds.interpolate(context.Background(), q, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := `SELECT $__m(), EXPANDED`
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestContextMacro_MissingCloseParen(t *testing.T) {
	ds := newDS(nil)
	ds.RegisterMacro("m", func(MacroContext, []string) (string, error) { return "", nil })
	q := &sqlutil.Query{RawSQL: `SELECT $__m(a, b`}
	_, err := ds.interpolate(context.Background(), q, nil)
	if !errors.Is(err, ErrorParsingMacroBrackets) {
		t.Fatalf("got %v want ErrorParsingMacroBrackets", err)
	}
}

// escapeLiteral is the canonical escape helper documented in MacroContext's
// GoDoc — duplicated here so the test stays self-contained.
func escapeLiteral(s string) string {
	out := make([]rune, 0, len(s))
	for _, r := range s {
		switch r {
		case '\\':
			out = append(out, '\\', '\\')
		case '\'':
			out = append(out, '\\', '\'')
		default:
			out = append(out, r)
		}
	}
	return string(out)
}
