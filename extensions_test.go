package sqlds

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/grafana/grafana-plugin-sdk-go/data/sqlutil"
)

// --- service registry ---------------------------------------------------

type metaKey struct{}
type otherKey struct{}

func TestRegisterResolve_RoundTrip(t *testing.T) {
	ds := &SQLDatasource{}
	ds.Register(metaKey{}, "provider-instance")
	got, ok := ds.Resolve(metaKey{})
	if !ok {
		t.Fatal("expected ok=true")
	}
	if got != "provider-instance" {
		t.Fatalf("got %v want %q", got, "provider-instance")
	}
}

func TestResolve_MissingKey(t *testing.T) {
	ds := &SQLDatasource{}
	if got, ok := ds.Resolve(metaKey{}); ok || got != nil {
		t.Fatalf("got (%v, %v) want (nil, false)", got, ok)
	}
}

func TestRegister_Overwrite(t *testing.T) {
	ds := &SQLDatasource{}
	ds.Register(metaKey{}, "first")
	ds.Register(metaKey{}, "second")
	got, _ := ds.Resolve(metaKey{})
	if got != "second" {
		t.Fatalf("got %v want %q", got, "second")
	}
}

func TestRegister_ConcurrentAccess(t *testing.T) {
	ds := &SQLDatasource{}
	var wg sync.WaitGroup
	const N = 200
	wg.Add(N * 2)
	for i := 0; i < N; i++ {
		go func(i int) {
			defer wg.Done()
			ds.Register(i, i*2)
		}(i)
		go func(i int) {
			defer wg.Done()
			_, _ = ds.Resolve(i)
		}(i)
	}
	wg.Wait()
	// Spot-check a few keys.
	for _, i := range []int{0, N / 2, N - 1} {
		if v, ok := ds.Resolve(i); !ok || v != i*2 {
			t.Fatalf("key %d: got (%v, %v) want (%d, true)", i, v, ok, i*2)
		}
	}
}

func TestRegister_DatasourceIsolation(t *testing.T) {
	a := &SQLDatasource{}
	b := &SQLDatasource{}
	a.Register(metaKey{}, "value-on-a")
	if _, ok := b.Resolve(metaKey{}); ok {
		t.Fatal("expected b.Resolve to miss after a.Register")
	}
}

func TestRegister_DistinctKeys(t *testing.T) {
	ds := &SQLDatasource{}
	ds.Register(metaKey{}, "m")
	ds.Register(otherKey{}, "o")
	if v, _ := ds.Resolve(metaKey{}); v != "m" {
		t.Fatalf("metaKey got %v", v)
	}
	if v, _ := ds.Resolve(otherKey{}); v != "o" {
		t.Fatalf("otherKey got %v", v)
	}
}

// --- MacroContext --------------------------------------------------------

func TestMacroContext_AccessorsPropagate(t *testing.T) {
	type providerKey struct{}
	ds := &SQLDatasource{}
	ds.Register(providerKey{}, "the-provider")

	type ctxKey struct{}
	ctx := context.WithValue(context.Background(), ctxKey{}, "ctx-val")
	query := &sqlutil.Query{RawSQL: "SELECT 1"}
	rawJSON := json.RawMessage(`{"rawSql":"SELECT 1","extra":42}`)

	mctx := &macroContext{
		ctx:     ctx,
		ds:      ds,
		query:   query,
		rawJSON: rawJSON,
		pos:     17,
	}

	if mctx.Context().Value(ctxKey{}) != "ctx-val" {
		t.Fatal("Context() did not propagate")
	}
	if mctx.Query() != query {
		t.Fatal("Query() did not propagate (pointer mismatch)")
	}
	if string(mctx.QueryJSON()) != string(rawJSON) {
		t.Fatalf("QueryJSON got %q", string(mctx.QueryJSON()))
	}
	if mctx.Pos() != 17 {
		t.Fatalf("Pos got %d want 17", mctx.Pos())
	}
	v, ok := mctx.Resolve(providerKey{})
	if !ok || v != "the-provider" {
		t.Fatalf("Resolve got (%v, %v)", v, ok)
	}
}
