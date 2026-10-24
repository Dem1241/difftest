package main

import (
	"fmt"
	"math/rand"
	"strings"
)

type table struct {
	name string
	cols []string
	nums []string
	strs []string
}

var tables = []table{
	{name: "t1", cols: []string{"id", "a", "b", "c", "d", "e", "u"}, nums: []string{"id", "a", "b", "d", "e", "u"}, strs: []string{"c"}},
	{name: "t2", cols: []string{"id", "a", "x", "c"}, nums: []string{"id", "a", "x"}, strs: []string{"c"}},
}

type gen struct {
	r     *rand.Rand
	alias int
	depth int
}

func genQuery(r *rand.Rand) string {
	g := &gen{r: r}
	shapes := []func() string{
		g.simpleSelect,
		g.existsQuery,
		g.inQuery,
		g.scalarSubquery,
		g.joinQuery,
		g.groupQuery,
		g.windowQuery,
		g.derivedQuery,
		g.lateralQuery,
		g.setOpQuery,
		g.distinctLimitQuery,
	}
	return shapes[r.Intn(len(shapes))]()
}

func (g *gen) pick(xs ...string) string { return xs[g.r.Intn(len(xs))] }

func (g *gen) newAlias() string {
	g.alias++
	return fmt.Sprintf("q%d", g.alias)
}

func (g *gen) table() table { return tables[g.r.Intn(len(tables))] }

func (g *gen) num(t table, a string) string { return a + "." + t.nums[g.r.Intn(len(t.nums))] }

func (g *gen) col(t table, a string) string { return a + "." + t.cols[g.r.Intn(len(t.cols))] }

func (g *gen) numConst() string {
	return g.pick("0", "1", "2", "-1", "3", "10", "0.5", "-2.25", "NULL", "4000000000", "CAST(1 AS UNSIGNED)", "1e1")
}

// numExpr builds a numeric expression over the columns of the given aliases.
func (g *gen) numExpr(scope map[string]table, depth int) string {
	a, t := g.anyIn(scope)
	if depth <= 0 || g.r.Intn(3) == 0 {
		if g.r.Intn(4) == 0 {
			return g.numConst()
		}
		return g.num(t, a)
	}
	l, r := g.numExpr(scope, depth-1), g.numExpr(scope, depth-1)
	switch g.r.Intn(9) {
	case 0, 1, 2:
		return fmt.Sprintf("(%s %s %s)", l, g.pick("+", "-", "*", "/", "DIV", "%", "MOD"), r)
	case 3:
		return fmt.Sprintf("COALESCE(%s, %s)", l, r)
	case 4:
		return fmt.Sprintf("IF(%s, %s, %s)", g.pred(scope, 0), l, r)
	case 5:
		return fmt.Sprintf("CASE WHEN %s THEN %s ELSE %s END", g.pred(scope, 0), l, r)
	case 6:
		return fmt.Sprintf("%s(%s)", g.pick("ABS", "FLOOR", "CEIL", "SIGN"), l)
	case 7:
		return fmt.Sprintf("ROUND(%s, %s)", l, g.pick("0", "1", "-1"))
	default:
		return fmt.Sprintf("NULLIF(%s, %s)", l, r)
	}
}

func (g *gen) anyIn(scope map[string]table) (string, table) {
	keys := make([]string, 0, len(scope))
	for k := range scope {
		keys = append(keys, k)
	}
	// sort for determinism under a fixed seed
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j] < keys[j-1]; j-- {
			keys[j], keys[j-1] = keys[j-1], keys[j]
		}
	}
	k := keys[g.r.Intn(len(keys))]
	return k, scope[k]
}

func (g *gen) pred(scope map[string]table, depth int) string {
	a, t := g.anyIn(scope)
	switch g.r.Intn(10) {
	case 0:
		return fmt.Sprintf("%s IS %sNULL", g.col(t, a), g.pick("", "NOT "))
	case 1:
		return fmt.Sprintf("%s BETWEEN %s AND %s", g.numExpr(scope, 1), g.numConst(), g.numConst())
	case 2:
		return fmt.Sprintf("%s %sIN (%s, %s, %s)", g.num(t, a), g.pick("", "NOT "), g.numConst(), g.numConst(), g.numConst())
	case 3:
		return fmt.Sprintf("%s %s %s", a+"."+t.strs[0], g.pick("=", "<>", "<", ">="), g.pick("'a'", "'B'", "''", "'10'", "NULL", "10"))
	case 4:
		if depth > 0 {
			return fmt.Sprintf("(%s %s %s)", g.pred(scope, depth-1), g.pick("AND", "OR"), g.pred(scope, depth-1))
		}
	case 5:
		if depth > 0 {
			return "NOT " + g.pred(scope, depth-1)
		}
	}
	return fmt.Sprintf("%s %s %s", g.numExpr(scope, 1), g.pick("=", "<>", "<", "<=", ">", ">=", "<=>"), g.numExpr(scope, 1))
}

// orderAll returns an ORDER BY that makes the order of the given columns deterministic
func orderAll(n int) string {
	parts := make([]string, n)
	for i := range parts {
		parts[i] = fmt.Sprint(i + 1)
	}
	return " ORDER BY " + strings.Join(parts, ", ")
}

func (g *gen) selectList(scope map[string]table, n int) []string {
	out := make([]string, n)
	for i := range out {
		if g.r.Intn(3) == 0 {
			a, t := g.anyIn(scope)
			out[i] = g.col(t, a)
		} else {
			out[i] = g.numExpr(scope, 2)
		}
	}
	return out
}

func (g *gen) simpleSelect() string {
	t, a := g.table(), g.newAlias()
	scope := map[string]table{a: t}
	sel := g.selectList(scope, 1+g.r.Intn(3))
	return fmt.Sprintf("SELECT %s FROM %s %s WHERE %s", strings.Join(sel, ", "), t.name, a, g.pred(scope, 2))
}

// innerSource returns a FROM item for a subquery: a table or a derived table that uses a window, a limit or a group by
func (g *gen) innerSource() (string, string, table) {
	t, a := g.table(), g.newAlias()
	switch g.r.Intn(5) {
	case 0:
		return fmt.Sprintf("(SELECT id, a, %s AS v FROM %s ORDER BY %s, id LIMIT %d) %s", g.pick("ROW_NUMBER() OVER (ORDER BY a, id)", "RANK() OVER (ORDER BY a)", "COUNT(*) OVER (PARTITION BY a)", "SUM(id) OVER (ORDER BY id)", "LAG(a) OVER (ORDER BY id)"), t.name, g.pick("a", "id"), 1+g.r.Intn(4), a), a, table{name: "dt", cols: []string{"id", "a", "v"}, nums: []string{"id", "a", "v"}, strs: []string{"a"}}
	case 1:
		return fmt.Sprintf("(SELECT id, a, %s AS v FROM %s) %s", g.pick("ROW_NUMBER() OVER (ORDER BY a, id)", "DENSE_RANK() OVER (ORDER BY a)", "MAX(id) OVER (PARTITION BY a)", "LEAD(id, 1, -1) OVER (ORDER BY id)"), t.name, a), a, table{name: "dt", cols: []string{"id", "a", "v"}, nums: []string{"id", "a", "v"}, strs: []string{"a"}}
	case 2:
		return fmt.Sprintf("(SELECT a AS id, a, COUNT(*) AS v FROM %s GROUP BY a) %s", t.name, a), a, table{name: "dt", cols: []string{"id", "a", "v"}, nums: []string{"id", "a", "v"}, strs: []string{"a"}}
	case 3:
		return fmt.Sprintf("(SELECT id, a, id AS v FROM %s ORDER BY id DESC LIMIT %d OFFSET %d) %s", t.name, 1+g.r.Intn(3), g.r.Intn(3), a), a, table{name: "dt", cols: []string{"id", "a", "v"}, nums: []string{"id", "a", "v"}, strs: []string{"a"}}
	}
	return t.name + " " + a, a, t
}

func (g *gen) existsQuery() string {
	t, a := g.table(), g.newAlias()
	src, ia, it := g.innerSource()
	scope := map[string]table{a: t, ia: it}
	corr := fmt.Sprintf("%s.%s = %s.%s", ia, g.pick("id", "a"), a, g.pick("id", "a"))
	extra := ""
	if g.r.Intn(2) == 0 {
		extra = " AND " + g.pred(scope, 1)
	}
	return fmt.Sprintf("SELECT %s.id FROM %s %s WHERE %sEXISTS (SELECT 1 FROM %s WHERE %s%s)", a, t.name, a, g.pick("", "NOT "), src, corr, extra)
}

func (g *gen) inQuery() string {
	t, a := g.table(), g.newAlias()
	src, ia, it := g.innerSource()
	scope := map[string]table{ia: it}
	where := ""
	if g.r.Intn(2) == 0 {
		where = " WHERE " + g.pred(scope, 1)
	}
	return fmt.Sprintf("SELECT %s.id FROM %s %s WHERE %s %sIN (SELECT %s FROM %s%s)", a, t.name, a, g.num(t, a), g.pick("", "NOT "), g.num(it, ia), src, where)
}

func (g *gen) scalarSubquery() string {
	t, a := g.table(), g.newAlias()
	it, ia := g.table(), g.newAlias()
	agg := g.pick("MAX", "MIN", "COUNT", "SUM", "AVG")
	return fmt.Sprintf("SELECT %s.id, (SELECT %s(%s) FROM %s %s WHERE %s.%s %s %s.%s) FROM %s %s", a, agg, g.num(it, ia), it.name, ia, ia, g.pick("a", "id"), g.pick("=", "<", ">="), a, g.pick("a", "id"), t.name, a)
}

func (g *gen) joinQuery() string {
	l, la := g.table(), g.newAlias()
	r, ra := g.table(), g.newAlias()
	scope := map[string]table{la: l, ra: r}
	join := g.pick("JOIN", "LEFT JOIN", "RIGHT JOIN", "LEFT JOIN", "CROSS JOIN")
	on := ""
	if join != "CROSS JOIN" {
		on = fmt.Sprintf(" ON %s.%s = %s.%s", la, g.pick("id", "a"), ra, g.pick("id", "a"))
		if g.r.Intn(3) == 0 {
			on += " AND " + g.pred(scope, 0)
		}
	}
	where := ""
	if g.r.Intn(2) == 0 {
		where = " WHERE " + g.pred(scope, 1)
	}
	sel := g.selectList(scope, 1+g.r.Intn(3))
	return fmt.Sprintf("SELECT %s FROM %s %s %s %s %s%s%s", strings.Join(sel, ", "), l.name, la, join, r.name, ra, on, where)
}

func (g *gen) groupQuery() string {
	t, a := g.table(), g.newAlias()
	scope := map[string]table{a: t}
	key := g.pick(a+".a", a+".c", fmt.Sprintf("(%s.id %% 3)", a), a+".a IS NULL")
	agg := fmt.Sprintf("%s(%s%s)", g.pick("COUNT", "SUM", "MIN", "MAX", "AVG", "BIT_OR", "STDDEV_POP"), g.pick("", "DISTINCT "), g.numExpr(scope, 1))
	if strings.HasPrefix(agg, "BIT_OR(DISTINCT") || strings.HasPrefix(agg, "STDDEV_POP(DISTINCT") {
		agg = strings.Replace(agg, "DISTINCT ", "", 1)
	}
	having := ""
	if g.r.Intn(2) == 0 {
		having = fmt.Sprintf(" HAVING COUNT(*) %s %s", g.pick(">", "=", "<="), g.pick("1", "2"))
	}
	where := ""
	if g.r.Intn(2) == 0 {
		where = " WHERE " + g.pred(scope, 1)
	}
	return fmt.Sprintf("SELECT %s, %s, COUNT(*) FROM %s %s%s GROUP BY %s%s", key, agg, t.name, a, where, key, having)
}

func (g *gen) windowQuery() string {
	t, a := g.table(), g.newAlias()
	scope := map[string]table{a: t}
	part := g.pick("", fmt.Sprintf("PARTITION BY %s.a ", a), fmt.Sprintf("PARTITION BY %s.c ", a))
	order := fmt.Sprintf("ORDER BY %s, %s.id", g.num(t, a), a)
	fn := g.pick(
		"ROW_NUMBER()", "RANK()", "DENSE_RANK()", "PERCENT_RANK()", "CUME_DIST()", "NTILE(3)",
		fmt.Sprintf("LAG(%s, 1)", g.num(t, a)), fmt.Sprintf("LEAD(%s, 2, 0)", g.num(t, a)),
		fmt.Sprintf("FIRST_VALUE(%s)", g.num(t, a)), fmt.Sprintf("LAST_VALUE(%s)", g.num(t, a)),
		fmt.Sprintf("SUM(%s)", g.num(t, a)), fmt.Sprintf("COUNT(%s)", g.col(t, a)), fmt.Sprintf("AVG(%s)", g.num(t, a)),
		fmt.Sprintf("MIN(%s)", g.num(t, a)),
	)
	frame := ""
	if strings.HasPrefix(fn, "SUM") || strings.HasPrefix(fn, "COUNT") || strings.HasPrefix(fn, "AVG") || strings.HasPrefix(fn, "MIN") || strings.HasPrefix(fn, "FIRST") || strings.HasPrefix(fn, "LAST") {
		frame = g.pick("", " ROWS BETWEEN 1 PRECEDING AND CURRENT ROW", " ROWS BETWEEN UNBOUNDED PRECEDING AND UNBOUNDED FOLLOWING", " ROWS BETWEEN CURRENT ROW AND 2 FOLLOWING")
	}
	where := ""
	if g.r.Intn(3) == 0 {
		where = " WHERE " + g.pred(scope, 1)
	}
	return fmt.Sprintf("SELECT %s.id, %s OVER (%s%s%s) FROM %s %s%s", a, fn, part, order, frame, t.name, a, where)
}

func (g *gen) derivedQuery() string {
	src, ia, it := g.innerSource()
	scope := map[string]table{ia: it}
	sel := g.selectList(scope, 1+g.r.Intn(2))
	return fmt.Sprintf("SELECT %s FROM %s WHERE %s", strings.Join(sel, ", "), src, g.pred(scope, 1))
}

func (g *gen) lateralQuery() string {
	t, a := g.table(), g.newAlias()
	it, ia := g.table(), g.newAlias()
	la := g.newAlias()
	inner := g.pick(
		fmt.Sprintf("SELECT %s.id AS v FROM %s %s WHERE %s.a = %s.a ORDER BY %s.id LIMIT 1", ia, it.name, ia, ia, a, ia),
		fmt.Sprintf("SELECT COUNT(*) AS v FROM %s %s WHERE %s.id < %s.id", it.name, ia, ia, a),
		fmt.Sprintf("SELECT %s.id + 1 AS v WHERE %s.a %s 1", a, a, g.pick("=", ">", "IS NOT")),
		fmt.Sprintf("SELECT MAX(%s.%s) AS v FROM %s %s WHERE %s.id <> %s.id", ia, g.pick("id", "a"), it.name, ia, ia, a),
	)
	return fmt.Sprintf("SELECT %s.id, %s.v FROM %s %s %s LATERAL (%s) %s%s", a, la, t.name, a, g.pick("JOIN", "LEFT JOIN", "CROSS JOIN"), inner, la, g.pick(" ON TRUE", " ON TRUE", " ON TRUE"))
}

func (g *gen) setOpQuery() string {
	l, la := g.table(), g.newAlias()
	r, ra := g.table(), g.newAlias()
	op := g.pick("UNION", "UNION ALL", "INTERSECT", "EXCEPT", "INTERSECT ALL", "EXCEPT ALL")
	return fmt.Sprintf("SELECT %s FROM %s %s WHERE %s %s SELECT %s FROM %s %s WHERE %s", g.num(l, la), l.name, la, g.pred(map[string]table{la: l}, 1), op, g.num(r, ra), r.name, ra, g.pred(map[string]table{ra: r}, 1))
}

func (g *gen) distinctLimitQuery() string {
	t, a := g.table(), g.newAlias()
	scope := map[string]table{a: t}
	sel := g.selectList(scope, 1+g.r.Intn(2))
	return fmt.Sprintf("SELECT DISTINCT %s FROM %s %s%s LIMIT %d", strings.Join(sel, ", "), t.name, a, orderAll(len(sel)), 1+g.r.Intn(4))
}
