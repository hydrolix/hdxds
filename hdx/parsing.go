package hdx

import (
	"strings"
	"time"

	"github.com/grafana/grafana-plugin-sdk-go/backend"
	"github.com/grafana/grafana-plugin-sdk-go/backend/log"
	"github.com/hydrolix/clickhouse-sql-parser/parser"
)

// MacroId identifies a macro occurrence in the parsed AST by name + byte
// position.
type MacroId struct {
	Name  string     `json:"name"`
	Index parser.Pos `json:"index"`
}

// CTE describes the table the AST-locatable macro applies to. AdHocFilter
// macros use CTE.Table / CTE.Database to look up which ad-hoc filter keys
// to apply.
type CTE struct {
	Macro    string     `json:"macro"`
	MacroPos parser.Pos `json:"macroPos"`
	CTE      string     `json:"cte"`
	Table    string     `json:"table"`
	Database string     `json:"database"`
	Pos      parser.Pos `json:"pos"`
}

// RoundTimeRange rounds the time range to the provided interval. Returns
// the input unchanged if the interval cannot be parsed or is sub-second.
func RoundTimeRange(timeRange backend.TimeRange, interval string) backend.TimeRange {
	if dInterval, err := time.ParseDuration(interval); err == nil && dInterval.Seconds() >= 1 {
		to := timeRange.To.Round(dInterval)
		from := timeRange.From.Round(dInterval)
		log.DefaultLogger.Debug("Time range rounded", "original", timeRange, "from", from, "to", to, "interval", interval)
		return backend.TimeRange{To: to, From: from}
	}
	log.DefaultLogger.Warn("Using default time range, provided round interval is invalid", "interval", interval)
	return timeRange
}

type macroVisitor struct {
	parser.DefaultASTVisitor
	macros []MacroId
}

func (v *macroVisitor) VisitIdent(expr *parser.Ident) error {
	if strings.HasPrefix(expr.Name, "$__") {
		v.macros = append(v.macros, MacroId{Name: expr.Name, Index: expr.NamePos})
	}
	return nil
}

type tableVisitor struct {
	parser.DefaultASTVisitor
	pos      parser.Pos
	table    string
	database string
}

func (v *tableVisitor) VisitTableIdentifier(expr *parser.TableIdentifier) error {
	if v.pos == expr.Pos() {
		if expr.Table != nil {
			v.table = expr.Table.Name
		}
		if expr.Database != nil {
			v.database = expr.Database.Name
		} else {
			v.database = ""
		}
	}
	return nil
}

// formatExpr renders an AST expression back to its source-form SQL string
// using the v0.5.1 parser.Formatter. Earlier parser releases exposed a
// direct String() method on Expr; v0.5.1 moved formatting behind the
// Formatter to keep the AST nodes free of stringification concerns.
func formatExpr(e parser.Expr) string {
	f := parser.NewFormatter()
	f.WriteExpr(e)
	return f.String()
}

type queryVisitor struct {
	parser.DefaultASTVisitor
	macroIds map[MacroId]CTE
}

func (v *queryVisitor) VisitSelectQuery(expr *parser.SelectQuery) error {
	if expr.From != nil {
		pos := expr.Pos()
		cte := formatExpr(expr.From.Expr)
		tPos := expr.From.Expr.Pos()
		tVisitor := tableVisitor{pos: tPos}
		_ = expr.Accept(&tVisitor)
		mVisitor := macroVisitor{macros: make([]MacroId, 0)}
		_ = expr.Accept(&mVisitor)
		for _, macro := range mVisitor.macros {
			if existing, ok := v.macroIds[macro]; !ok || existing.Pos < pos {
				v.macroIds[macro] = CTE{
					Macro:    macro.Name,
					MacroPos: macro.Index,
					CTE:      cte,
					Pos:      pos,
					Database: tVisitor.database,
					Table:    tVisitor.table,
				}
			}
		}
	}
	return nil
}

// GetMacroCTEs walks an AST and returns the (MacroId → CTE) mapping that
// AdHocFilterMacro uses to look up which table a given macro invocation
// applies to.
func GetMacroCTEs(ast []parser.Expr) (map[MacroId]CTE, error) {
	visitor := queryVisitor{macroIds: make(map[MacroId]CTE)}
	for _, expr := range ast {
		if err := expr.Accept(&visitor); err != nil {
			return nil, err
		}
	}
	return visitor.macroIds, nil
}
