# difftest

runs randomly generated queries against [go-mysql-server](https://github.com/dolthub/go-mysql-server) (the engine in dolt) and a real mysql server, and writes every query whose result differs to a file.

```sh
go build -tags gms_pure_go -o difftest .
./difftest -mysql 'root@unix(/path/to/mysql.sock)/' -n 3000 -seed 1 -out diffs.txt
```

the database uses `utf8mb4_0900_bin` on both sides, since that's the go-mysql-server default and mysql's default is case insensitive. numbers are compared by value, and differences that are only formatting are reported as `format-diff`. every `ORDER BY` inside a window, `LIMIT` or derived table ends with a unique key, so both sides have exactly one right answer.

`go.mod` points go-mysql-server at a local checkout with a `replace`, so known bugs can be filtered out by testing a branch that already has their fixes.
