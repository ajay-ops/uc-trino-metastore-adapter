# Unity Catalog v0.5.1 → HMS mapping

Source-derived contract for external Delta tables, Trino 472. Runtime JVM/Go wire verification remains a mandatory implementation gate. Milestone 3 implements the schema and external Delta table mappings below. Go unit/TCP/golden wire tests pass; real-HMS comparison and live Trino SQL acceptance remain pending.

## UC API contract

Use one configured UC catalog (`unity` in examples) per adapter deployment and Trino catalog. Base URL ends in `/api/2.1/unity-catalog`. Catalog mapping is adapter configuration, not `hive.metastore.thrift.catalog-name`.

| Read | Path / parameters | Response |
|---|---|---|
| Schemas | `GET /schemas?catalog_name=unity&max_results=100&page_token=...` | `ListSchemasResponse.schemas`, `next_page_token` |
| Schema | `GET /schemas/unity.raw_bdp` | `SchemaInfo` |
| Tables | `GET /tables?catalog_name=unity&schema_name=raw_bdp&max_results=50&page_token=...` | `ListTablesResponse.tables`, `next_page_token` |
| Table | `GET /tables/unity.raw_bdp.account_info` | `TableInfo` |

`SchemaInfo` has name, catalog_name, full_name, comment, properties, owner, audit fields, schema_id, storage_root and storage_location. `TableInfo` has name, catalog_name, schema_name, table_type, data_source_format, columns, storage_location, comment, properties, owner, audit fields, table_id and view-related fields. These response schemas do not declare a required field list: validate fields needed by the adapter explicitly. TableInfo has no `full_name` field in this tag. `ColumnInfo` includes name, type_text/type_json/type_name, numeric/interval details, position, nullable, comment and partition_index. [U-api](https://github.com/unitycatalog/unitycatalog/blob/v0.5.1/api/all.yaml)

GET table has documented streaming/materialized-view coercion flags, but the ordinary `TableService.getTable` simply retrieves the table. Do not rely on those flags to turn view-like tables into safe Delta tables. Admit **table_type=EXTERNAL AND data_source_format=DELTA** only; explicitly reject malformed/unknown types and missing locations. Managed support is a later separately approved contract. [U-api](https://github.com/unitycatalog/unitycatalog/blob/v0.5.1/api/all.yaml) [U-table](https://github.com/unitycatalog/unitycatalog/blob/v0.5.1/server/src/main/java/io/unitycatalog/server/service/TableService.java)

List table results already contain TableInfo, so table-meta generation should not require N additional GETs. Validate identity and eligibility on every page. UC response filtering happens after repository pagination: an empty filtered page can still have a next token. Server helper default page size is 100; OpenAPI maxima are 1000 for schemas and 50 for tables. Use explicit compliant positive page sizes, opaque tokens and token-based termination, not page length. A full final page can produce an extra empty page. Do not depend on current lexicographic token implementation or claim snapshot-isolated pagination across requests. [U-page](https://github.com/unitycatalog/unitycatalog/blob/v0.5.1/server/src/main/java/io/unitycatalog/server/persist/utils/PagedListingHelper.java) [U-schema](https://github.com/unitycatalog/unitycatalog/blob/v0.5.1/server/src/main/java/io/unitycatalog/server/service/SchemaService.java) [U-table](https://github.com/unitycatalog/unitycatalog/blob/v0.5.1/server/src/main/java/io/unitycatalog/server/service/TableService.java)

URL-encode path and query values with standard URL facilities; never concatenate untrusted query strings. Validate namespace components and returned names. Inventory mixed-case/dotted/escaped identifiers before adopting them: do not silently lowercase distinct UC objects or invent escaping for ambiguous full names.

## SchemaInfo → Database

IDL field numbers below are wire IDs, not struct order. Exact structure: [H2](https://github.com/trinodb/hive-thrift/blob/2/src/main/thrift/hive_metastore.thrift) `Database`. Trino consumer: [T-convert](https://github.com/trinodb/trino/blob/472/plugin/trino-hive/src/main/java/io/trino/plugin/hive/metastore/thrift/ThriftMetastoreUtil.java) `fromMetastoreApiDatabase`; optional schema properties: [T-metadata](https://github.com/trinodb/trino/blob/472/plugin/trino-delta-lake/src/main/java/io/trino/plugin/deltalake/DeltaLakeMetadata.java) `getSchemaProperties`.

| HMS field | Value | Reason |
|---|---|---|
| `1 name` | UC `name` | Required by Trino database builder |
| `2 description` | UC `comment`, or empty string | Optional display comment; not Delta schema authority |
| `3 locationUri` | UC `storage_location`, or empty string | Schema storage location only; do not fabricate warehouse paths or derive table locations from it. Trino treats empty as absent |
| `4 parameters` | Empty non-null map initially | No UC property is required by these read paths; no blind copying |
| `5 privileges` | Unset | No HMS authorization claims |
| `6 ownerName`, `7 ownerType` | Both unset initially | UC owner is not a trusted HMS principal mapping. Trino falls back to PUBLIC/ROLE. Never set a name without a valid type |
| `8 catalogName` | Unset | Native HMS catalog override is outside initial profile |

UC schema properties/owner remain UC metadata; the adapter does not export them as grants or default storage ownership. If ownership display becomes a requirement, define principal mapping before populating both owner fields.

## TableInfo → Table / GetTableResult

Trino first runs `BridgingHiveMetastore.getTable`, including Avro/CSV checks, and `ThriftMetastoreUtil.fromMetastoreApiTable`. Only afterward does the Delta wrapper validate provider and use the SerDe path. Provider + path alone are therefore **not structurally sufficient**. [T-bridge](https://github.com/trinodb/trino/blob/472/plugin/trino-hive/src/main/java/io/trino/plugin/hive/metastore/thrift/BridgingHiveMetastore.java) [T-convert](https://github.com/trinodb/trino/blob/472/plugin/trino-hive/src/main/java/io/trino/plugin/hive/metastore/thrift/ThriftMetastoreUtil.java) [T-wrapper](https://github.com/trinodb/trino/blob/472/plugin/trino-delta-lake/src/main/java/io/trino/plugin/deltalake/metastore/HiveMetastoreBackedDeltaLakeMetastore.java)

| HMS Table field | Initial contract | Trino consumption |
|---|---|---|
| `1 tableName`, `2 dbName` | Validated UC `name`, `schema_name` | Table identity |
| `3 owner` | Empty string | Converted to optional owner; not used to authorize our external-table read contract |
| `4 createTime`, `5 lastAccessTime`, `6 retention` | Zero | No audit/retention authority is implied; milliseconds are not blindly cast into i32 seconds |
| `7 sd` | Non-null descriptor below | Missing descriptor is HIVE_INVALID_METADATA |
| `8 partitionKeys` | Explicit non-null empty list | Converter calls `.stream()`; actual partition schema comes from Delta |
| `9 parameters` | Exactly `{"spark.sql.sources.provider":"DELTA"}` initially | Delta validation is case-insensitive; normalize constant output |
| `10 viewOriginalText`, `11 viewExpandedText` | Unset where generator permits, otherwise empty | Not a view; Trino normalizes empty text |
| `12 tableType` | `EXTERNAL_TABLE` | Correct table kind; managed flag stays false |
| `13 privileges` | Unset | No fabricated permissions |
| `14 temporary`, `15 rewriteEnabled` | Default false/unset | No temporary/MV behavior |
| `16 creationMetadata`, `17 catName` | Unset | No MV/HMS catalog semantics |
| `18 ownerType` | IDL default USER/unset | Does not carry UC authorization |
| `19 writeId` | IDL default -1/unset | No Hive ACID transaction ID |

`get_table_req` wraps the identical object in **GetTableResult field 1, required Table**; `get_table` returns it directly. Do not conflate UC table_id with an HMS write ID. [H2](https://github.com/trinodb/hive-thrift/blob/2/src/main/thrift/hive_metastore.thrift)

| StorageDescriptor field | Initial contract |
|---|---|
| `1 cols` | Explicit non-null empty `list<FieldSchema>` |
| `2 location` | Exact UC `storage_location`, unchanged |
| `3 inputFormat`, `4 outputFormat` | Empty string; Trino's `StorageFormat.createNullable` tolerates absent format classes. No Hive SerDe is instantiated by native Delta reads |
| `5 compressed` | false (placeholder, not Parquet compression metadata) |
| `6 numBuckets` | 0; avoids Hive bucketing |
| `7 serdeInfo` | Non-null SerDeInfo below |
| `8 bucketCols`, `9 sortCols` | Explicit empty lists |
| `10 parameters` | Empty map |
| `11 skewedInfo`, `12 storedAsSubDirectories` | Unset / false |

| SerDeInfo field | Initial contract |
|---|---|
| `1 name` | UC table name |
| `2 serializationLib` | Empty string; specifically not Avro/CSV |
| `3 parameters` | `{"path": "<exact UC storage_location>"}` |
| `4..7` description, serializer/deserializer classes, serdeType | Unset |

`sd.location` and SerDe `path` both carry the same unmodified location. The **latter is the one Trino's Delta wrapper reads**. No scheme rewriting (including `s3a`→`s3`), trailing slash cleanup or URI decode/re-encode of stored object keys. Validate supported scheme/bucket against the deployment without opening S3 in the adapter. Unsupported location fails explicitly; data remains untouched. [T-wrapper](https://github.com/trinodb/trino/blob/472/plugin/trino-delta-lake/src/main/java/io/trino/plugin/deltalake/metastore/HiveMetastoreBackedDeltaLakeMetastore.java) [T-convert](https://github.com/trinodb/trino/blob/472/plugin/trino-hive/src/main/java/io/trino/plugin/hive/metastore/thrift/ThriftMetastoreUtil.java)

`fromMetastoreApiTable` consumes data/partition column lists, names, owner, type, parameters, optional view text and writeId. Storage conversion consumes SerDe library, input/output format, location, bucketing, skew info and SerDe parameters. Empty lists and numBuckets=0 avoid extra Hive type/bucket parsing. Delta then needs provider, non-view table kind and SerDe path. These statements are source-derived; a real generated Go→Java round trip must prove empty lists are emitted rather than omitted. [T-convert](https://github.com/trinodb/trino/blob/472/plugin/trino-hive/src/main/java/io/trino/plugin/hive/metastore/thrift/ThriftMetastoreUtil.java)

Do not forward UC properties wholesale. In particular, omit Avro schema markers, view markers, transactional markers, and Trino's cached Delta schema/version metadata (`DeltaLakeTableMetadataScheduler`). A UC property must not override the synthesized provider/path or make the adapter a second schema authority. [T-bridge](https://github.com/trinodb/trino/blob/472/plugin/trino-hive/src/main/java/io/trino/plugin/hive/metastore/thrift/BridgingHiveMetastore.java) [T-scheduler](https://github.com/trinodb/trino/blob/472/plugin/trino-delta-lake/src/main/java/io/trino/plugin/deltalake/metastore/DeltaLakeTableMetadataScheduler.java)

## TableInfo → TableMeta

| Wire field | Value |
|---|---|
| `1 dbName` (required) | UC schema_name |
| `2 tableName` (required) | UC name |
| `3 tableType` (required) | `EXTERNAL_TABLE` |
| `4 comments` (optional) | UC comment, if present |
| `5 catName` (optional) | Unset |

The bridge converts names and calls `TableInfo.ExtendedRelationType.fromTableTypeAndComment`. The external type produces TABLE regardless of comments. Do not label views as external Delta to make listing succeed. [T-bridge](https://github.com/trinodb/trino/blob/472/plugin/trino-hive/src/main/java/io/trino/plugin/hive/metastore/thrift/BridgingHiveMetastore.java) [T-tableinfo](https://github.com/trinodb/trino/blob/472/lib/trino-metastore/src/main/java/io/trino/metastore/TableInfo.java)

## Column policy

Use **empty HMS columns and partitionKeys**, not UC columns and not fabricated dummy columns, for the initial table RPCs. The inspected Trino conversion has no non-empty-column requirement; native Delta `getTableMetadata/getColumnHandles/streamTableColumns` reads the transaction log. This minimizes duplicate schema authority and avoids stale UC columns breaking Hive type parsing. [T-convert](https://github.com/trinodb/trino/blob/472/plugin/trino-hive/src/main/java/io/trino/plugin/hive/metastore/thrift/ThriftMetastoreUtil.java) [T-metadata](https://github.com/trinodb/trino/blob/472/plugin/trino-delta-lake/src/main/java/io/trino/plugin/deltalake/DeltaLakeMetadata.java)

`get_fields` is not required and will remain unsupported. If a later client genuinely requires it, choose and document semantics separately: a UC-based FieldSchema would map name→name, parsed Hive-compatible type_text→type, comment→comment, sorted by position. It must reject unrepresentable types; it must not claim UC types reflect the current Delta snapshot. No such converter belongs in phase 1.

## Authentication and failures

`all.yaml` does not specify a bearer security scheme for these routes. When authorization is enabled, `UnityCatalogServer.addSecurityDecorators` installs `AuthDecorator`: it reads Bearer token/cookie, requires issuer INTERNAL, verifies signature, and requires an enabled UC user by subject email. An arbitrary external IdP JWT is **not** accepted directly as the table API access token. Provision/exchange a UC-issued service-identity token outside the adapter's metadata logic; define rotation and least-privilege grants operationally. [U-server](https://github.com/unitycatalog/unitycatalog/blob/v0.5.1/server/src/main/java/io/unitycatalog/server/UnityCatalogServer.java) [U-auth](https://github.com/unitycatalog/unitycatalog/blob/v0.5.1/server/src/main/java/io/unitycatalog/server/service/AuthDecorator.java)

Table GET privileges are defined by `AuthorizeExpressions.GET_TABLE`; a non-owner reader generally needs USE_CATALOG, USE_SCHEMA and SELECT on the table. Listing also applies response authorization filters. Validate list and GET grants with the actual UC service principal; a 200 list may intentionally omit inaccessible objects. HMS setUGI does not change that principal. [U-authz](https://github.com/unitycatalog/unitycatalog/blob/v0.5.1/server/src/main/java/io/unitycatalog/server/auth/AuthorizeExpressions.java) [U-schema](https://github.com/unitycatalog/unitycatalog/blob/v0.5.1/server/src/main/java/io/unitycatalog/server/service/SchemaService.java) [U-table](https://github.com/unitycatalog/unitycatalog/blob/v0.5.1/server/src/main/java/io/unitycatalog/server/service/TableService.java)

Exact failure translation and declared Thrift exceptions are in [RPC_MATRIX.md](RPC_MATRIX.md#wire-error-contract). Never use empty lists or NoSuchObjectException for an upstream outage.
