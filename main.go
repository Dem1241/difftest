// difftest runs randomly generated queries against go-mysql-server or dolt and
// a real MySQL server and reports every query whose results differ.
package main

import (
	"context"
	gosql "database/sql"
	"flag"
	"fmt"
	"math/rand"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	_ "github.com/go-sql-driver/mysql"

	sqle "github.com/dolthub/go-mysql-server"
	"github.com/dolthub/go-mysql-server/memory"
	"github.com/dolthub/go-mysql-server/server"
	"github.com/dolthub/go-mysql-server/sql"
	"github.com/dolthub/go-mysql-server/sql/analyzer"
	"github.com/dolthub/go-mysql-server/sql/variables"
)

const dbName = "difftest"

var setup = []string{
	"CREATE TABLE t1 (id INT PRIMARY KEY, a INT, b INT, c VARCHAR(10), d DECIMAL(6,2), e DOUBLE, u INT UNSIGNED)",
	"CREATE TABLE t2 (id INT PRIMARY KEY, a INT, x INT, c VARCHAR(10))",
	`INSERT INTO t1 VALUES
		(1, 1, 10, 'a', 1.50, 0.5, 1),
		(2, 1, 20, 'B', -2.25, -1.5, 2),
		(3, 2, NULL, NULL, 0.00, 2.25, 3),
		(4, NULL, 30, 'a', 10.10, NULL, NULL),
		(5, 3, 10, '10', NULL, 3.5, 4000000000),
		(6, -1, -10, '', 99.99, 0, 0),
		(7, 2, 20, 'c', -0.01, 1e10, 7),
		(8, 0, 0, 'b', 5.00, -0.25, 8)`,
	`INSERT INTO t2 VALUES
		(1, 1, 100, 'a'),
		(2, 2, NULL, 'b'),
		(3, NULL, 300, NULL),
		(4, 3, 100, 'a'),
		(5, 1, -100, 'B'),
		(6, 7, 0, '')`,
}

func main() {
	mysqlDSN := flag.String("mysql", "root@unix(/tmp/mysql.sock)/", "DSN of the MySQL server to compare against")
	doltDSN := flag.String("dolt", "", "DSN of a running dolt sql-server to test instead of the in-process go-mysql-server")
	port := flag.Int("port", 33070, "port for the go-mysql-server instance")
	n := flag.Int("n", 1000, "number of queries to run")
	seed := flag.Int64("seed", time.Now().UnixNano(), "random seed")
	out := flag.String("out", "diffs.txt", "file to write differences to")
	flag.Parse()

	name, dsn := "dolt", *doltDSN
	if dsn == "" {
		name, dsn = "gms", fmt.Sprintf("root@tcp(127.0.0.1:%d)/", *port)
		if err := startGMS(*port); err != nil {
			fail(err)
		}
	}
	engine, err := connect(dsn)
	if err != nil {
		fail(err)
	}
	my, err := connect(*mysqlDSN)
	if err != nil {
		fail(err)
	}
	for _, db := range []*gosql.DB{engine, my} {
		if err := load(db); err != nil {
			fail(err)
		}
	}

	f, err := os.Create(*out)
	if err != nil {
		fail(err)
	}
	defer f.Close()

	rng := rand.New(rand.NewSource(*seed))
	fmt.Printf("seed %d\n", *seed)
	counts := map[string]int{}
	for i := 0; i < *n; i++ {
		q := genQuery(rng)
		eRes := run(engine, q)
		mRes := run(my, q)
		kind := compare(name, eRes, mRes)
		counts[kind]++
		if kind == "same" || kind == "both-error" {
			continue
		}
		fmt.Fprintf(f, "### %s\n%s;\n-- %s: %s\n-- mysql: %s\n\n", kind, q, name, eRes, mRes)
	}
	fmt.Println(counts)
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}

func startGMS(port int) error {
	vars := variables.NewEngineVariables()
	pro := memory.NewDBProvider(memory.NewDatabase(dbName))
	engine := sqle.New(analyzer.NewDefault(pro), &sqle.Config{EngineVariables: vars})
	cfg := server.Config{Protocol: "tcp", Address: fmt.Sprintf("127.0.0.1:%d", port)}
	s, err := server.NewServer(cfg, engine, sql.ContextFactoryWithVariables(vars), memory.NewSessionBuilderWithVariables(pro, vars), nil)
	if err != nil {
		return err
	}
	go func() { _ = s.Start() }()
	time.Sleep(200 * time.Millisecond)
	return nil
}

func connect(dsn string) (*gosql.DB, error) {
	db, err := gosql.Open("mysql", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	return db, db.Ping()
}

func load(db *gosql.DB) error {
	stmts := append([]string{
		"DROP DATABASE IF EXISTS " + dbName,
		"CREATE DATABASE " + dbName + " COLLATE utf8mb4_0900_bin",
		"USE " + dbName,
	}, setup...)
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			return fmt.Errorf("%s: %w", s, err)
		}
	}
	return nil
}

type result struct {
	err  string
	rows []string
	// raw holds the rows exactly as the server sent them, rows holds them with numbers normalized
	raw []string
}

func (r result) String() string {
	if r.err != "" {
		return "ERROR " + r.err
	}
	if len(r.rows) > 12 {
		return fmt.Sprintf("%d rows %v ...", len(r.rows), r.rows[:12])
	}
	return fmt.Sprintf("%d rows %v", len(r.rows), r.rows)
}

func run(db *gosql.DB, q string) result {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	rows, err := db.QueryContext(ctx, q)
	if err != nil {
		return result{err: oneLine(err.Error())}
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return result{err: oneLine(err.Error())}
	}
	var res result
	for rows.Next() {
		vals := make([]gosql.RawBytes, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return result{err: oneLine(err.Error())}
		}
		fields := make([]string, len(vals))
		rawFields := make([]string, len(vals))
		for i, v := range vals {
			if v == nil {
				fields[i] = "NULL"
				rawFields[i] = "NULL"
			} else {
				fields[i] = normalize(string(v))
				rawFields[i] = string(v)
			}
		}
		res.rows = append(res.rows, "("+strings.Join(fields, ",")+")")
		res.raw = append(res.raw, "("+strings.Join(rawFields, ",")+")")
	}
	if err := rows.Err(); err != nil {
		return result{err: oneLine(err.Error())}
	}
	sort.Strings(res.rows)
	sort.Strings(res.raw)
	return res
}

func oneLine(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) > 160 {
		s = s[:160]
	}
	return s
}

// normalize formats numbers the same way on both sides, so that 1e+10 and
// 10000000000 compare equal. Differences that are only formatting are
// reported separately as format-diff.
func normalize(s string) string {
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return s
	}
	if f == 0 {
		return "0"
	}
	return strconv.FormatFloat(f, 'g', 15, 64)
}

func compare(name string, g, m result) string {
	switch {
	case g.err != "" && m.err != "":
		return "both-error"
	case g.err != "":
		lower := strings.ToLower(g.err)
		if strings.Contains(lower, "not supported") || strings.Contains(lower, "unsupported") || strings.Contains(lower, "not yet implemented") || strings.Contains(lower, "syntax error") {
			return name + "-unsupported"
		}
		return name + "-error"
	case m.err != "":
		return "mysql-error"
	case strings.Join(g.rows, "|") != strings.Join(m.rows, "|"):
		return "result-diff"
	case strings.Join(g.raw, "|") != strings.Join(m.raw, "|"):
		return "format-diff"
	}
	return "same"
}
