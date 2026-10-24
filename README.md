# difftest

runs randomly generated queries against [go-mysql-server](https://github.com/dolthub/go-mysql-server), the sql engine inside [dolt](https://github.com/dolthub/dolt), and a real mysql server side by side, and writes every query whose result differs to a file.

the queries go after the parts of a sql engine that are easy to get wrong: correlated `EXISTS` / `IN` subqueries, derived tables with windows and `LIMIT`, lateral joins, outer joins, `NULL` in `IN` / `NOT IN`, mixed signed / unsigned / decimal / double arithmetic, `GROUP BY` with `HAVING`, window frames, `DISTINCT` with `ORDER BY` and `LIMIT`, and set operations.

## running it

you need go and a mysql 8 server to compare against.

```sh
go build -tags gms_pure_go -o difftest .
./difftest -mysql 'root@unix(/path/to/mysql.sock)/' -n 3000 -seed 1 -out diffs.txt
```

it starts go-mysql-server in-process on port 33070 (`-port`), creates a `difftest` database with two small tables on both servers, and prints a count per outcome:

- `same`: both returned the same rows
- `result-diff`: different rows, the interesting one
- `format-diff`: same values, printed differently (`1e+10` vs `10000000000`)
- `gms-error` / `mysql-error`: only one side failed
- `gms-unsupported`: go-mysql-server doesn't support the syntax
- `both-error`: both failed

## how it avoids false positives

- the database uses `utf8mb4_0900_bin` on both sides. that's the go-mysql-server default, and mysql's default collation is case insensitive, which otherwise shows up as a difference in every `GROUP BY` or `PARTITION BY` on a string.
- every `ORDER BY` inside a window function, `LIMIT` or derived table ends with a unique key, so there is exactly one right answer.
- rows are compared as a sorted multiset, and numbers by value.

## testing a branch

to check a fix, or to filter out bugs that already have a fix waiting, point go-mysql-server at a local checkout:

```sh
go mod edit -replace github.com/dolthub/go-mysql-server=../go-mysql-server
```

## what it found

- [dolthub/go-mysql-server#4065](https://github.com/dolthub/go-mysql-server/pull/4065): `SELECT DISTINCT CEIL(a) FROM t ORDER BY 1 LIMIT 3` failed with an internal error, and similar queries returned the wrong rows
- [dolthub/go-mysql-server#4066](https://github.com/dolthub/go-mysql-server/pull/4066): `SELECT COUNT(1), COUNT(*) FROM t` failed
- [dolthub/go-mysql-server#4067](https://github.com/dolthub/go-mysql-server/pull/4067): `UNION`, `INTERSECT` and `EXCEPT` treated `5` and `5.00` as different values
- it also found [dolthub/dolt#11548](https://github.com/dolthub/dolt/issues/11548) again, a correlated `EXISTS` over a derived table with `LIMIT` returning wrong rows, fixed in [dolthub/go-mysql-server#4061](https://github.com/dolthub/go-mysql-server/pull/4061)

## license

MIT
