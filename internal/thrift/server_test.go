package thrift

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/ajay-ops/uc-trino-metastore-adapter/internal/config"
	"github.com/ajay-ops/uc-trino-metastore-adapter/internal/observability"
	hms "github.com/ajay-ops/uc-trino-metastore-adapter/internal/thrift/generated"
	apache "github.com/apache/thrift/lib/go/thrift"
)

func startServer(t *testing.T, limit int) (*Server, *observability.Metrics) {
	t.Helper()
	metrics := observability.NewMetrics()
	cfg := config.Config{ThriftAddress: "127.0.0.1:0", MaxConnections: limit, MaxMessageBytes: 4096, SocketTimeout: time.Second, OperationTimeout: time.Second}
	server, err := New(cfg, observability.NewLogger(io.Discard, 0), metrics, nil)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- server.Serve() }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()
		_ = server.Shutdown(ctx)
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("accept loop did not stop")
		}
	})
	return server, metrics
}

func connect(t *testing.T, address string) (*apache.TSocket, *hms.ThriftHiveMetastoreClient) {
	t.Helper()
	transport := apache.NewTSocketConf(address, &apache.TConfiguration{ConnectTimeout: time.Second, SocketTimeout: time.Second})
	if err := transport.Open(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = transport.Close() })
	return transport, hms.NewThriftHiveMetastoreClientFactory(transport, apache.NewTBinaryProtocolFactoryDefault())
}

func TestEveryOtherIDLMethodRejected(t *testing.T) {
	source, err := os.ReadFile("../../third_party/hive-thrift/hive_metastore.thrift")
	if err != nil {
		t.Fatal(err)
	}
	clean := regexp.MustCompile(`(?s)/\*.*?\*/|//[^\n]*`).ReplaceAllString(string(source), "")
	service := strings.SplitN(clean, "service ThriftHiveMetastore", 2)[1]
	methods := regexp.MustCompile(`\b(\w+)\s*\(`).FindAllStringSubmatch(service, -1)
	server, _ := startServer(t, 4)
	transport, _ := connect(t, server.Addr().String())
	protocol := apache.NewTBinaryProtocolFactoryDefault().GetProtocol(transport)
	allowed := hms.NewThriftHiveMetastoreProcessor(&Handler{}).ProcessorMap()
	seen := map[string]bool{}
	for _, method := range append(methods, []string{"", "future_unknown_method"}) {
		name := method[1]
		if name == "throws" || seen[name] {
			continue
		}
		seen[name] = true
		if _, ok := allowed[name]; ok {
			continue
		}
		assertUnknown(t, protocol, name)
	}
	if len(seen) < 100 {
		t.Fatalf("IDL inventory unexpectedly small: %d", len(seen))
	}
}

func assertUnknown(t *testing.T, p apache.TProtocol, name string) {
	t.Helper()
	ctx := context.Background()
	seq := int32(42)
	steps := []func() error{
		func() error { return p.WriteMessageBegin(ctx, name, apache.CALL, seq) },
		func() error { return p.WriteStructBegin(ctx, "args") },
		func() error { return p.WriteFieldStop(ctx) }, func() error { return p.WriteStructEnd(ctx) },
		func() error { return p.WriteMessageEnd(ctx) }, func() error { return p.Flush(ctx) },
	}
	for _, step := range steps {
		if err := step(); err != nil {
			t.Fatal(err)
		}
	}
	response, kind, id, err := p.ReadMessageBegin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if response != name || kind != apache.EXCEPTION || id != seq {
		t.Fatalf("bad envelope: %s %d %d", response, kind, id)
	}
	exception := apache.NewTApplicationException(apache.UNKNOWN_APPLICATION_EXCEPTION, "")
	if err := exception.Read(ctx, p); err != nil {
		t.Fatal(err)
	}
	if exception.TypeId() != apache.UNKNOWN_METHOD {
		t.Fatalf("%s: wrong exception %v", name, exception)
	}
	if err := p.ReadMessageEnd(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestShutdownClosesIdleConnections(t *testing.T) {
	server, _ := startServer(t, 4)
	transport, client := connect(t, server.Addr().String())
	_, _ = client.GetTableReq(context.Background(), &hms.GetTableRequest{DbName: "raw", TblName: "t", CatName: new("unsupported")}) // Establish an accepted socket.
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	err := server.Shutdown(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected forced idle drain: %v", err)
	}
	if _, err := client.GetTableReq(context.Background(), &hms.GetTableRequest{DbName: "raw", TblName: "t", CatName: new("unsupported")}); err == nil {
		t.Fatal("socket survived shutdown")
	}
	_ = transport.Close()
	if _, err := net.DialTimeout("tcp", server.Addr().String(), 100*time.Millisecond); err == nil {
		t.Fatal("listener still accepting")
	}
}

func TestConnectionLimitAndMalformedInput(t *testing.T) {
	server, _ := startServer(t, 1)
	first, client := connect(t, server.Addr().String())
	_, _ = client.GetTableReq(context.Background(), &hms.GetTableRequest{DbName: "raw", TblName: "t", CatName: new("unsupported")})
	second, err := net.Dial("tcp", server.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	_ = second.SetReadDeadline(time.Now().Add(time.Second))
	var b [1]byte
	if _, err := second.Read(b[:]); err == nil {
		t.Fatal("excess connection not rejected")
	} else if timeout, ok := err.(net.Error); ok && timeout.Timeout() {
		t.Fatal("excess connection left open")
	}
	_ = first.Close()
	// Malformed/truncated clients must not panic or prevent a fresh connection.
	conn, err := net.Dial("tcp", server.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	_, _ = conn.Write([]byte{0x80, 0x01})
	_ = conn.Close()
}
