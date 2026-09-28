# Contract tests

`go test ./...` runs the independent field-ID fixtures in `wire_test.go` and
`table_wire_test.go`, including optional skew-field skipping and explicit table
container/provider/path encoding. `internal/thrift/*_test.go` exercises all five
read RPCs through real TCP sockets and controlled UC HTTP fixtures, including
not-found, timeout/outage, pagination, fallback parity and rejection of every
non-allowlisted upstream method. [Golden fixtures](golden/README.md) are synthetic,
not captured HMS responses.

Live Trino integration was deferred by the owner during Milestone 2. It has not
been resumed. Pending acceptance is Trino 472 with UC 0.5.1 and a valid Delta log:
SHOW SCHEMAS, SHOW TABLES, DESCRIBE and upstream-failure behavior, without a real
HMS configured or reachable. The adapter itself never reads Delta files. See
[Milestone 3](../docs/MILESTONE_3.md) for actual results and limitations.
