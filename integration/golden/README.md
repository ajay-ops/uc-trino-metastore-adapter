# Delta metadata golden fixtures

These fixtures are **synthetic**, authored from the approved
[UC/HMS field mapping](../../docs/UC_HMS_MAPPING.md), UC 0.5.1's API and the
checksum-pinned Trino `hive-thrift:2` IDL. They are not captured real HMS responses.
No sanitized real HMS Delta response was available in the repository for this
milestone, so real-HMS comparison remains pending.

- `uc_external_delta.json`: external Delta table with an unchanged path containing
  a space, percent escape and trailing slash. Deliberately stale UC columns and
  conflicting properties test that they do not become HMS schema/provider data.
- `hms_external_delta.expected.json`: reviewed expected adapter shape, including
  non-null empty containers and generated defaults (USER owner type and writeId -1).
  Optional owner type/writeId defaults may be omitted on the Thrift wire; JSON
  reflects the decoded Go object. Empty containers must actually be transmitted.

`internal/translate/table_test.go` compares the complete generated object with the
static golden. `internal/thrift/table_test.go` compares both lookup RPCs after TCP
serialization/deserialization, and `integration/table_wire_test.go` independently
checks upstream field IDs/types and explicit container presence.

A future real-HMS fixture must include provenance (HMS/Spark/Delta versions), be
sanitized without changing field presence, and represent the same UC table. Compare
semantic compatibility through Trino rather than raw byte equality: old HMS columns,
SerDe class and Spark properties may legitimately differ. The adapter intentionally
leaves schema columns empty so DESCRIBE follows `_delta_log`.
