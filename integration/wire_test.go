package integration_test

import (
	"context"
	"testing"

	hms "github.com/ajay-ops/uc-trino-metastore-adapter/internal/thrift/generated"
	apache "github.com/apache/thrift/lib/go/thrift"
)

// Write using upstream wire field IDs, independently of the projected Go
// writer. Hive's optional skew map must be skipped, not retyped or misdecoded.
func TestOriginalSkewedDescriptorCanBeDecoded(t *testing.T) {
	ctx := context.Background()
	buffer := apache.NewTMemoryBufferLen(256)
	p := apache.NewTBinaryProtocolFactoryDefault().GetProtocol(buffer)
	check := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	check(p.WriteStructBegin(ctx, "StorageDescriptor"))
	check(p.WriteFieldBegin(ctx, "skewedInfo", apache.STRUCT, 11))
	check(p.WriteStructBegin(ctx, "SkewedInfo"))
	check(p.WriteFieldBegin(ctx, "skewedColValueLocationMaps", apache.MAP, 3))
	check(p.WriteMapBegin(ctx, apache.LIST, apache.STRING, 1))
	check(p.WriteListBegin(ctx, apache.STRING, 1))
	check(p.WriteString(ctx, "key"))
	check(p.WriteListEnd(ctx))
	check(p.WriteString(ctx, "s3://unused/skew"))
	check(p.WriteMapEnd(ctx))
	check(p.WriteFieldEnd(ctx))
	check(p.WriteFieldStop(ctx))
	check(p.WriteStructEnd(ctx))
	check(p.WriteFieldEnd(ctx))
	check(p.WriteFieldBegin(ctx, "location", apache.STRING, 2))
	check(p.WriteString(ctx, "s3://unchanged/table"))
	check(p.WriteFieldEnd(ctx))
	check(p.WriteFieldStop(ctx))
	check(p.WriteStructEnd(ctx))
	descriptor := hms.NewStorageDescriptor()
	check(descriptor.Read(ctx, p))
	if descriptor.Location != "s3://unchanged/table" {
		t.Fatalf("decoder lost alignment after unknown skew field: %q", descriptor.Location)
	}
}

func TestGetTableRequestUsesOriginalFieldIDs(t *testing.T) {
	ctx := context.Background()
	buffer := apache.NewTMemoryBufferLen(128)
	p := apache.NewTBinaryProtocolFactoryDefault().GetProtocol(buffer)
	check := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	check(p.WriteStructBegin(ctx, "GetTableRequest"))
	for _, field := range []struct {
		id    int16
		value string
	}{{1, "raw"}, {2, "account"}, {4, "catalog"}} {
		check(p.WriteFieldBegin(ctx, "", apache.STRING, field.id))
		check(p.WriteString(ctx, field.value))
		check(p.WriteFieldEnd(ctx))
	}
	check(p.WriteFieldStop(ctx))
	check(p.WriteStructEnd(ctx))
	request := hms.NewGetTableRequest()
	check(request.Read(ctx, p))
	if request.DbName != "raw" || request.TblName != "account" || request.GetCatName() != "catalog" {
		t.Fatalf("incorrect request mapping: %+v", request)
	}
}
