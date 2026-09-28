# Hive Metastore wire provenance

`hive_metastore.thrift` is the **unchanged** source from
[trinodb/hive-thrift tag 2](https://github.com/trinodb/hive-thrift/blob/2/src/main/thrift/hive_metastore.thrift).

SHA-256: `b4a4be314077eeafe487d2c004d7dcea949141a2f716dd86ff01c00e58651575`.
The upstream Apache license is included as `LICENSE`.

This corresponds exactly to the Java client artifact **io.trino.hive:hive-thrift:2**
used by [Trino 472's POM](https://github.com/trinodb/trino/blob/472/pom.xml).
It is Trino's Hive Metastore API snapshot, with Hive 3-era request APIs and catalog
fields. Tag `2` is an artifact version, **not Hive server version 2**. The upstream
project does not identify this file as an unmodified Apache Hive 3.1.3 release IDL;
we do not claim that equivalence. The existing HMS 3.1.3 is a future compatibility
oracle, not the adapter's code dependency. No HMS server or backing database is used.

## Projection and generation

`python3 scripts/generate-thrift.py` checks the upstream digest, copies the five
approved method signatures and their transitive type declarations, and generates
`internal/thrift/idl/hms.thrift` plus `internal/thrift/generated/*.go`.
The Go namespace is `hms`. Generated source is checked in; normal builds require
Go, not the Thrift compiler. `make check-generated` verifies reproducibility.
Use **Apache Thrift compiler 0.24.0**, Go runtime **github.com/apache/thrift v0.24.0**,
and the repository's Go toolchain (tested with 1.27.1).

The compiler rejects Hive's `map<list<string>,string>` in `SkewedInfo`: Go slices
cannot be map keys. The projection therefore omits **optional StorageDescriptor
field 11 (`skewedInfo`)** and its now-unreferenced type. All other retained fields,
wire names, IDs, types, defaults, requiredness and exception slots are preserved.
The approved external-Delta mapping never emits this optional field. Standard
Thrift readers skip unknown fields; `integration/wire_test.go` verifies decoding
an upstream-shaped descriptor containing the original skew map followed by another
field. We never retype its map keys or modify the upstream file/generated Go.

This narrows the projected model; it does not support arbitrary Hive skewed tables.
The projection is tied to the checksum-pinned grammar, not a general IDL parser.
Updating the source/compiler requires explicit projection review and wire tests.

Milestone 3 implements all five projected read RPCs. Every other RPC uses Apache
protocol primitives to return `TApplicationException(UNKNOWN_METHOD)`. No mutation
can succeed. Go socket tests verify rejection for every non-allowlisted method in
the full upstream IDL. Synthetic golden tests verify both table lookup responses
and independent field-ID checks verify non-null empty collections/provider/path.
No IDL or generated code changed for Milestone 3. Live Trino 472/483 Java conversion
and real-HMS comparison remain unverified.
