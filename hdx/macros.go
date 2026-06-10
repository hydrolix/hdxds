// SECURITY: Every user-supplied value that this file interpolates into SQL
// is wrapped in a single-quoted literal and run through escapeSQLLiteral,
// which doubles backslashes then escapes single quotes. The fork's
// equivalent file (hydrolix/sqlds@1738cf0:macros.go) interpolated user
// values into ClickHouse dollar-quoted literals ($value$), which have no
// defined escape sequence and were therefore vulnerable to SQL injection
// when a value contained a `$` followed by the closing `$$`. The port to
// `'…'` with explicit escaping closes that hole. Do not reintroduce
// dollar-quoted literal interpolation here.
package hdx

import (
	"context"
	"fmt"
	"maps"
	"math"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/grafana/grafana-plugin-sdk-go/backend"
	"github.com/grafana/grafana-plugin-sdk-go/data/sqlutil"
	"github.com/hydrolix/clickhouse-sql-parser/parser"
)

const (
	SyntheticNull  = "__null__"
	SyntheticEmpty = "__empty__"
	RegexPrefix    = "regex:"
)

var mapTypeFilterKey = regexp.MustCompile(`^(.*)\['.*']$`)

// HDXMacroFunc is the HDX macro signature, parallel to the upstream
// sqlutil.MacroFunc but carrying the HDXQuery, the AST-derived position
// and the metadata provider. Renamed from the fork's MacroFunc to avoid
// the existing upstream alias `MacroFunc = sqlutil.MacroFunc`.
type HDXMacroFunc func(ctx context.Context, query *HDXQuery, args []string, pos parser.Pos, mdp *MetaDataProvider) (string, error)

// escapeSQLLiteral makes a string safe to embed inside a single-quoted SQL
// literal: backslashes are doubled, then single quotes are escaped with a
// backslash. Apply to every user-supplied value before wrapping it in '…'.
//
// Order matters: backslash first, then single quote, otherwise the backslash
// inserted by the quote-escape step would itself get doubled.
func escapeSQLLiteral(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `'`, `\'`)
	return s
}

// quoteLiteral wraps escapeSQLLiteral with the surrounding quotes for
// terse use inside fmt.Sprintf.
func quoteLiteral(s string) string {
	return "'" + escapeSQLLiteral(s) + "'"
}

func timeToDate(t time.Time) string {
	return fmt.Sprintf("toDate('%s')", t.Format("2006-01-02"))
}

func timeToDateTime(t time.Time) string {
	return fmt.Sprintf("toDateTime(%d)", t.Unix())
}

func timeToDateTime64(t time.Time) string {
	return fmt.Sprintf("fromUnixTimestamp64Milli(%d)", t.UnixMilli())
}

func FromTimeFilter(_ context.Context, query *HDXQuery, _ []string, _ parser.Pos, _ *MetaDataProvider) (string, error) {
	return timeToDateTime(query.TimeRange.From), nil
}

func ToTimeFilter(_ context.Context, query *HDXQuery, _ []string, _ parser.Pos, _ *MetaDataProvider) (string, error) {
	return timeToDateTime(query.TimeRange.To), nil
}

func FromTimeFilterMs(_ context.Context, query *HDXQuery, _ []string, _ parser.Pos, _ *MetaDataProvider) (string, error) {
	return timeToDateTime64(query.TimeRange.From), nil
}

func ToTimeFilterMs(_ context.Context, query *HDXQuery, _ []string, _ parser.Pos, _ *MetaDataProvider) (string, error) {
	return timeToDateTime64(query.TimeRange.To), nil
}

func TimeFilter(ctx context.Context, query *HDXQuery, args []string, pos parser.Pos, mdp *MetaDataProvider) (string, error) {
	if len(args) > 1 {
		return "", backend.DownstreamError(fmt.Errorf("%w: expected 0 or 1 argument, received %d", sqlutil.ErrorBadArgumentCount, len(args)))
	}
	column, err := resolveColumnArg(ctx, args, query, pos, mdp)
	if err != nil {
		return "", err
	}
	from := query.TimeRange.From
	to := query.TimeRange.To
	return fmt.Sprintf("%s >= %s AND %s <= %s", column, timeToDateTime(from), column, timeToDateTime(to)), nil
}

func TimeFilterMs(ctx context.Context, query *HDXQuery, args []string, pos parser.Pos, mdp *MetaDataProvider) (string, error) {
	if len(args) > 1 {
		return "", backend.DownstreamError(fmt.Errorf("%w: expected 0 or 1 argument, received %d", sqlutil.ErrorBadArgumentCount, len(args)))
	}
	column, err := resolveColumnArg(ctx, args, query, pos, mdp)
	if err != nil {
		return "", err
	}
	from := query.TimeRange.From
	to := query.TimeRange.To
	return fmt.Sprintf("%s >= %s AND %s <= %s", column, timeToDateTime64(from), column, timeToDateTime64(to)), nil
}

func DateFilter(_ context.Context, query *HDXQuery, args []string, _ parser.Pos, _ *MetaDataProvider) (string, error) {
	if len(args) != 1 {
		return "", backend.DownstreamError(fmt.Errorf("%w: expected 1 argument, received %d", sqlutil.ErrorBadArgumentCount, len(args)))
	}
	column := args[0]
	from := query.TimeRange.From
	to := query.TimeRange.To
	return fmt.Sprintf("%s >= %s AND %s <= %s", column, timeToDate(from), column, timeToDate(to)), nil
}

func DateTimeFilter(_ context.Context, query *HDXQuery, args []string, _ parser.Pos, _ *MetaDataProvider) (string, error) {
	if len(args) != 2 {
		return "", backend.DownstreamError(fmt.Errorf("%w: expected 2 arguments, received %d", sqlutil.ErrorBadArgumentCount, len(args)))
	}
	dateColumn := args[0]
	timeColumn := args[1]
	from := query.TimeRange.From
	to := query.TimeRange.To
	dateFilter := fmt.Sprintf("(%s >= %s AND %s <= %s)", dateColumn, timeToDate(from), dateColumn, timeToDate(to))
	timeFilter := fmt.Sprintf("(%s >= %s AND %s <= %s)", timeColumn, timeToDateTime(from), timeColumn, timeToDateTime(to))
	return fmt.Sprintf("%s AND %s", dateFilter, timeFilter), nil
}

func TimeInterval(ctx context.Context, query *HDXQuery, args []string, pos parser.Pos, mdp *MetaDataProvider) (string, error) {
	if len(args) > 1 {
		return "", backend.DownstreamError(fmt.Errorf("%w: expected 0 or 1 argument, received %d", sqlutil.ErrorBadArgumentCount, len(args)))
	}
	column, err := resolveColumnArg(ctx, args, query, pos, mdp)
	if err != nil {
		return "", err
	}
	seconds := math.Max(query.Interval.Seconds(), 1)
	return fmt.Sprintf("toStartOfInterval(toDateTime(%s), INTERVAL %d second)", column, int(seconds)), nil
}

func TimeIntervalMs(ctx context.Context, query *HDXQuery, args []string, pos parser.Pos, mdp *MetaDataProvider) (string, error) {
	if len(args) > 1 {
		return "", backend.DownstreamError(fmt.Errorf("%w: expected 0 or 1 argument, received %d", sqlutil.ErrorBadArgumentCount, len(args)))
	}
	column, err := resolveColumnArg(ctx, args, query, pos, mdp)
	if err != nil {
		return "", err
	}
	milliseconds := math.Max(float64(query.Interval.Milliseconds()), 1)
	return fmt.Sprintf("toStartOfInterval(toDateTime64(%s, 3), INTERVAL %d millisecond)", column, int(milliseconds)), nil
}

func IntervalSeconds(_ context.Context, query *HDXQuery, _ []string, _ parser.Pos, _ *MetaDataProvider) (string, error) {
	seconds := math.Max(query.Interval.Seconds(), 1)
	return fmt.Sprintf("%d", int(seconds)), nil
}

// AdHocFilterMacro implements the $__adHocFilter() macro.
func AdHocFilterMacro(ctx context.Context, query *HDXQuery, params []string, pos parser.Pos, mdp *MetaDataProvider) (string, error) {
	if len(query.Filters) == 0 {
		return "1=1", nil
	}
	if len(params) > 1 {
		return "", backend.DownstreamError(fmt.Errorf("%w: expected 0 or 1 argument, received %d", sqlutil.ErrorBadArgumentCount, len(params)))
	}

	cte := ""
	if len(params) == 1 {
		cte = params[0]
	}

	if cte == "" {
		expr, err := parser.NewParser(query.RawSQL).ParseStmts()
		if err != nil {
			return "", err
		}
		macroCTEs, err := GetMacroCTEs(expr)
		if err != nil {
			return "", err
		}
		for _, macroCTE := range macroCTEs {
			if macroCTE.MacroPos == pos {
				cte = macroCTE.CTE
				break
			}
		}
	}
	if cte == "" {
		return "", fmt.Errorf("cannot apply ad hoc filters: unable to resolve tableName for ad hoc filter at index %d", pos)
	}
	keys, err := mdp.GetKeys(ctx, query.Headers, cte)
	if err != nil {
		return "", fmt.Errorf("cannot apply ad hoc filters: unable to resolve keys for cte: %s", cte)
	}

	var conditions []string
	keyNames := slices.Collect(maps.Keys(keys))
	for _, filter := range query.Filters {
		column := filter.Key
		if mapTypeFilterKey.MatchString(filter.Key) {
			column = mapTypeFilterKey.FindStringSubmatch(filter.Key)[1]
		}
		if slices.Contains(keyNames, column) {
			keyType := keys[column]
			condition, err := buildFilterCondition(filter, keyType)
			if err != nil {
				return "", fmt.Errorf("error building filter condition for key '%s': %w", filter.Key, err)
			}
			if condition != "" {
				conditions = append(conditions, condition)
			}
		}
	}
	if len(conditions) == 0 {
		return "1=1", nil
	}
	return strings.Join(conditions, " AND "), nil
}

func buildArrayCondition(filter AdHocFilter) (string, error) {
	key := filter.Key
	value := filter.Value
	operator := filter.Operator
	switch operator {
	case "=|":
		var buffer []string
		for _, v := range filter.Values {
			buffer = append(buffer, fmt.Sprintf("has(%s, %s)", key, quoteLiteral(v)))
		}
		return fmt.Sprintf("(%s)", strings.Join(buffer, " OR ")), nil
	case "!=|":
		var buffer []string
		for _, v := range filter.Values {
			buffer = append(buffer, fmt.Sprintf("not has(%s, %s)", key, quoteLiteral(v)))
		}
		return fmt.Sprintf("(%s)", strings.Join(buffer, " OR ")), nil
	case "!=":
		return fmt.Sprintf("not has(%s, %s)", key, quoteLiteral(value)), nil
	case "=":
		return fmt.Sprintf("has(%s, %s)", key, quoteLiteral(value)), nil
	default:
		return "", fmt.Errorf("operator %s unsupported for Array value", operator)
	}
}

// buildFilterCondition creates a SQL condition from an ad-hoc filter.
func buildFilterCondition(filter AdHocFilter, keyType string) (string, error) {
	isString := strings.Contains(strings.ToLower(keyType), "string)") || strings.ToLower(keyType) == "string"
	isArray := strings.Contains(strings.ToLower(keyType), "array")
	isMap := strings.Contains(strings.ToLower(keyType), "map")
	if isArray {
		return buildArrayCondition(filter)
	}

	key := filter.Key
	value := filter.Value
	operator := filter.Operator
	switch {
	case operator == "=|":
		if isMap && !isString {
			return "", fmt.Errorf("cannot apply =| operator over non string map values")
		}
		values, hasNull := getJoinedValues(filter.Values)
		var parts []string
		if hasNull {
			parts = append(parts, fmt.Sprintf("%s IS NULL", key))
		}
		if values != "" {
			parts = append(parts, fmt.Sprintf("%s IN (%s)", key, values))
		}
		switch len(parts) {
		case 0:
			return "", nil
		case 1:
			return parts[0], nil
		default:
			return fmt.Sprintf("(%s)", strings.Join(parts, " OR ")), nil
		}
	case operator == "!=|":
		if isMap && !isString {
			return "", fmt.Errorf("cannot apply !=| operator over non string map values")
		}
		values, hasNull := getJoinedValues(filter.Values)
		var parts []string
		if hasNull {
			parts = append(parts, fmt.Sprintf("%s IS NOT NULL", key))
		}
		if values != "" {
			parts = append(parts, fmt.Sprintf("%s NOT IN (%s)", key, values))
		}
		return strings.Join(parts, " AND "), nil
	case strings.ToUpper(value) == "NULL" || value == SyntheticNull:
		switch {
		case operator == "=" && isString:
			return fmt.Sprintf("(%s IS NULL OR %s = '%s')", key, key, SyntheticNull), nil
		case operator == "!=" && isString:
			return fmt.Sprintf("(%s IS NOT NULL OR %s != '%s')", key, key, SyntheticNull), nil
		case operator == "=":
			return fmt.Sprintf("%s IS NULL", key), nil
		case operator == "!=":
			return fmt.Sprintf("%s IS NOT NULL", key), nil
		default:
			return "", fmt.Errorf("%s: operator '%s' can not be applied to NULL value", key, operator)
		}
	case value == "" || value == SyntheticEmpty:
		switch operator {
		case "=":
			return fmt.Sprintf("(%s = '' OR %s = '%s')", key, key, SyntheticEmpty), nil
		case "!=":
			return fmt.Sprintf("(%s != '' AND %s != '%s')", key, key, SyntheticEmpty), nil
		default:
			return "", fmt.Errorf("%s: operator '%s' can not be applied to __empty__ value", key, operator)
		}
	case operator == "=~":
		regex, isRegex := getRegexValue(value)
		if isRegex {
			return fmt.Sprintf("match(toString(%s), %s)", key, quoteLiteral(regex)), nil
		}
		return fmt.Sprintf("toString(%s) LIKE %s", key, quoteLiteral(escapeWildcard(value))), nil
	case operator == "!~":
		regex, isRegex := getRegexValue(value)
		if isRegex {
			return fmt.Sprintf("not match(toString(%s), %s)", key, quoteLiteral(regex)), nil
		}
		return fmt.Sprintf("toString(%s) NOT LIKE %s", key, quoteLiteral(escapeWildcard(value))), nil
	default:
		return fmt.Sprintf("%s %s %s", key, operator, quoteLiteral(value)), nil
	}
}

func getRegexValue(value string) (string, bool) {
	if strings.HasPrefix(value, RegexPrefix) {
		return value[len(RegexPrefix):], true
	}
	return "", false
}

func getJoinedValues(values []string) (string, bool) {
	var buffer []string
	hasNull := false
	for _, v := range values {
		if strings.ToUpper(v) == "NULL" || v == SyntheticNull {
			hasNull = true
		} else if v == SyntheticEmpty {
			buffer = append(buffer, "''")
		} else {
			buffer = append(buffer, quoteLiteral(v))
		}
	}
	return strings.Join(buffer, ", "), hasNull
}

func escapeWildcard(v string) string {
	chars := []rune(v)
	for i := range len(chars) {
		if chars[i] == '*' && (i == 0 || chars[i-1] != '\\') {
			chars[i] = '%'
		}
	}
	v = string(chars)
	v = strings.ReplaceAll(v, `\*`, "*")
	return v
}

// Stub macros are intentional no-ops that resolve to the always-true
// predicate `1=1`. The fork uses this for `$__conditionalAll`.
func Stub(_ context.Context, _ *HDXQuery, _ []string, _ parser.Pos, _ *MetaDataProvider) (string, error) {
	return "1=1", nil
}

// resolveColumnArg returns the explicit column name when args[0] is set,
// otherwise consults the metadata provider for the table's primary key at
// the macro position.
func resolveColumnArg(ctx context.Context, args []string, query *HDXQuery, pos parser.Pos, mdp *MetaDataProvider) (string, error) {
	if len(args) == 1 && args[0] != "" {
		return args[0], nil
	}
	return getPK(ctx, query.RawSQL, pos, mdp, query.Headers)
}

func getPK(ctx context.Context, rawSQL string, pos parser.Pos, mdp *MetaDataProvider, headers http.Header) (string, error) {
	expr, err := parser.NewParser(rawSQL).ParseStmts()
	if err != nil {
		return rawSQL, err
	}
	macroIds, err := GetMacroCTEs(expr)
	if err != nil {
		return rawSQL, err
	}
	var cte *CTE
	for _, macroCTE := range macroIds {
		if macroCTE.MacroPos == pos {
			c := macroCTE
			cte = &c
			break
		}
	}
	if cte == nil {
		return rawSQL, fmt.Errorf("no CTE found for macro at pos %d", pos)
	}
	return mdp.GetPK(ctx, headers, cte.Database, cte.Table)
}

// Macros is the registry of HDX macros. Names match the bareword after `$__`
// in source SQL.
var Macros = map[string]HDXMacroFunc{
	"adHocFilter":     AdHocFilterMacro,
	"conditionalAll":  Stub,
	"fromTime":        FromTimeFilter,
	"toTime":          ToTimeFilter,
	"fromTime_ms":     FromTimeFilterMs,
	"toTime_ms":       ToTimeFilterMs,
	"timeFilter":      TimeFilter,
	"timeFilter_ms":   TimeFilterMs,
	"dateFilter":      DateFilter,
	"dateTimeFilter":  DateTimeFilter,
	"dt":              DateTimeFilter,
	"timeInterval":    TimeInterval,
	"timeInterval_ms": TimeIntervalMs,
	"interval_s":      IntervalSeconds,
}
