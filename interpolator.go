package sqlds

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/grafana/grafana-plugin-sdk-go/data/sqlutil"
)

// Interpolator owns the SQL rewriting pipeline for a datasource: it expands
// registered macros, fires the per-query hooks, and produces the SQL that
// reaches the driver. Plugins replace the default by assigning a custom
// value to SQLDatasource.Interpolator.
//
// Implementations MUST be safe for concurrent use across queries.
type Interpolator interface {
	Interpolate(ctx context.Context, ds *SQLDatasource, query *sqlutil.Query, rawJSON json.RawMessage) (string, error)
}

// DefaultInterpolator is the implementation used when SQLDatasource.Interpolator
// is nil. It:
//
//   - invokes ds.PreInterpolate exactly once before any macro fires;
//   - expands new-style context macros registered via ds.RegisterMacro,
//     supplying each invocation with a MacroContext that exposes the byte
//     offset of the macro in the source SQL;
//   - expands legacy macros via sqlutil.Interpolate, preserving today's
//     behaviour byte-for-byte when no context macros are registered;
//   - invokes ds.PostInterpolate exactly once on the rewritten SQL.
type DefaultInterpolator struct{}

// Interpolate implements Interpolator.
func (DefaultInterpolator) Interpolate(ctx context.Context, ds *SQLDatasource, query *sqlutil.Query, rawJSON json.RawMessage) (string, error) {
	if ds != nil && ds.PreInterpolate != nil {
		if err := ds.PreInterpolate(ctx, query, rawJSON); err != nil {
			return "", err
		}
	}

	// Phase 1: expand new-style context macros. We own this pass so we can
	// supply each invocation with its byte offset in the original SQL. If
	// no context macros are registered we skip this entirely, and the
	// legacy path below is bit-for-bit equivalent to today's behaviour.
	sql := query.RawSQL
	if ds != nil {
		if macros := ds.contextMacrosSnapshot(); len(macros) > 0 {
			expanded, err := expandContextMacros(ctx, ds, query, rawJSON, sql, macros)
			if err != nil {
				return "", err
			}
			sql = expanded
		}
	}

	// Phase 2: expand legacy macros via sqlutil.Interpolate, using a copy
	// of the query so we don't mutate the caller's RawSQL.
	legacyMacros := sqlutil.Macros{}
	if ds != nil {
		for name, fn := range ds.driver().Macros() {
			legacyMacros[name] = fn
		}
	}
	q2 := *query
	q2.RawSQL = sql
	rewritten, err := sqlutil.Interpolate(&q2, legacyMacros)
	if err != nil {
		return "", err
	}

	if ds != nil && ds.PostInterpolate != nil {
		rewritten, err = ds.PostInterpolate(ctx, query, rewritten)
		if err != nil {
			return "", err
		}
	}
	return rewritten, nil
}

// interpolate is the datasource-level entry point that resolves a nil
// Interpolator field to DefaultInterpolator and forwards the call. The
// internal QueryData path uses this; plugins that have already migrated
// can also call ds.Interpolator directly.
func (ds *SQLDatasource) interpolate(ctx context.Context, query *sqlutil.Query, rawJSON json.RawMessage) (string, error) {
	interp := ds.Interpolator
	if interp == nil {
		interp = DefaultInterpolator{}
	}
	return interp.Interpolate(ctx, ds, query, rawJSON)
}

// macroInvocation describes a single $__macroName(args) occurrence in the
// source SQL.
type macroInvocation struct {
	name string
	fn   ContextMacroFunc
	pos  int // byte offset of the leading $ in sql
	end  int // byte offset of the closing paren + 1
	args []string
}

// expandContextMacros finds and expands every $__<name>(args) occurrence
// whose name is registered in macros. Occurrences are replaced from highest
// position to lowest so prior positions remain stable as the string shrinks
// or grows. Escaped invocations (`$$__name`) are stripped of the leading
// dollar, matching sqlutil.Interpolate's escape convention.
func expandContextMacros(ctx context.Context, ds *SQLDatasource, query *sqlutil.Query, rawJSON json.RawMessage, sql string, macros map[string]ContextMacroFunc) (string, error) {
	// Build a single regex matching any of the registered macro names so we
	// scan the SQL once. Sort longer names first to prevent a shorter name
	// from claiming a match that a longer name should win (e.g. `time`
	// vs. `timeFilter`).
	names := make([]string, 0, len(macros))
	for n := range macros {
		names = append(names, n)
	}
	sort.Slice(names, func(i, j int) bool { return len(names[i]) > len(names[j]) })
	alternation := make([]string, 0, len(names))
	for _, n := range names {
		alternation = append(alternation, regexp.QuoteMeta(n))
	}
	rgx, err := regexp.Compile(`\$+__(` + strings.Join(alternation, "|") + `)\b`)
	if err != nil {
		return "", err
	}

	var invocations []macroInvocation
	var escapes []int // positions of leading dollar in escaped `$$__name`
	for _, w := range rgx.FindAllStringSubmatchIndex(sql, -1) {
		start, nameEnd := w[0], w[3]
		// w[2]..w[3] is the captured macro name.
		name := sql[w[2]:nameEnd]
		// Escaped form: $$__name — strip one leading $ and skip macro
		// expansion, matching sqlutil.Interpolate semantics.
		if start+1 < len(sql) && sql[start+1] == '$' {
			escapes = append(escapes, start)
			continue
		}
		args, length := parseMacroArgs(sql[nameEnd:])
		if length < 0 {
			return "", fmt.Errorf("%w: macro %s", ErrorParsingMacroBrackets, name)
		}
		invocations = append(invocations, macroInvocation{
			name: name,
			fn:   macros[name],
			pos:  start,
			end:  nameEnd + length,
			args: args,
		})
	}

	// Expand from highest pos to lowest so earlier positions stay valid
	// as we splice.
	sort.Slice(invocations, func(i, j int) bool { return invocations[i].pos > invocations[j].pos })
	for _, inv := range invocations {
		mctx := &macroContext{
			ctx:     ctx,
			ds:      ds,
			query:   query,
			rawJSON: rawJSON,
			pos:     inv.pos,
		}
		replacement, err := inv.fn(mctx, inv.args)
		if err != nil {
			return "", err
		}
		sql = sql[:inv.pos] + replacement + sql[inv.end:]
	}

	// Strip the leading dollar from each escaped occurrence (descending so
	// earlier positions stay valid).
	sort.Sort(sort.Reverse(sort.IntSlice(escapes)))
	for _, p := range escapes {
		sql = sql[:p] + sql[p+1:]
	}
	return sql, nil
}

// parseMacroArgs reads a bracketed argument list at the start of s and
// returns the whitespace-trimmed args plus the length of the consumed
// substring (including the surrounding parentheses). A leading character
// that is not `(` produces (nil, 0) — the macro takes no arguments. An
// unterminated argument list produces (nil, -1).
func parseMacroArgs(s string) ([]string, int) {
	if !strings.HasPrefix(s, "(") {
		return nil, 0
	}
	var args []string
	depth := 0
	cur := []rune{}
	for i, r := range s {
		switch r {
		case '(':
			depth++
			if depth == 1 {
				continue
			}
		case ')':
			depth--
			if depth == 0 {
				args = append(args, strings.TrimSpace(string(cur)))
				return args, i + 1
			}
		case ',':
			if depth == 1 {
				args = append(args, strings.TrimSpace(string(cur)))
				cur = []rune{}
				continue
			}
		}
		cur = append(cur, r)
	}
	return nil, -1
}
