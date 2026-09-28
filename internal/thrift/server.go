package thrift

import (
	"context"
	"log/slog"
	"net"
	"sync"
	"time"

	"github.com/ajay-ops/uc-trino-metastore-adapter/internal/config"
	"github.com/ajay-ops/uc-trino-metastore-adapter/internal/observability"
	hms "github.com/ajay-ops/uc-trino-metastore-adapter/internal/thrift/generated"
	"github.com/ajay-ops/uc-trino-metastore-adapter/internal/unity"
	apache "github.com/apache/thrift/lib/go/thrift"
)

// Server owns an unframed binary HMS listener and bounded connection lifetime.
type Server struct {
	server    *apache.TSimpleServer
	transport *serverTransport
	stopOnce  sync.Once
	stopped   chan struct{}
}

// New binds the listener synchronously, so readiness never precedes a successful bind.
func New(cfg config.Config, logger *slog.Logger, metrics *observability.Metrics, uc *unity.Client) (*Server, error) {
	listener, err := net.Listen("tcp", cfg.ThriftAddress)
	if err != nil {
		return nil, err
	}
	transport := &serverTransport{listener: listener, connections: make(map[*trackedConn]struct{}), limit: cfg.MaxConnections, metrics: metrics,
		wire: &apache.TConfiguration{SocketTimeout: cfg.SocketTimeout, MaxMessageSize: cfg.MaxMessageBytes, MaxFrameSize: cfg.MaxMessageBytes}}
	generated := hms.NewThriftHiveMetastoreProcessor(&Handler{UC: uc})
	processor := &observedProcessor{TProcessor: generated, logger: logger, metrics: metrics, timeout: cfg.OperationTimeout}
	srv := apache.NewTSimpleServer4(processor, transport, apache.NewTTransportFactory(), apache.NewTBinaryProtocolFactoryConf(transport.wire))
	srv.SetLogContext(context.Background())
	return &Server{server: srv, transport: transport, stopped: make(chan struct{})}, nil
}

func (s *Server) Addr() net.Addr { return s.transport.listener.Addr() }

// Serve returns listener failures instead of discarding AcceptLoop errors.
func (s *Server) Serve() error { return s.server.AcceptLoop() }

// Shutdown stops accepts, drains existing connections, and forcibly closes them
// if the caller's deadline expires. It does not mutate Thrift's process-global timeout.
func (s *Server) Shutdown(ctx context.Context) error {
	s.stopOnce.Do(func() { go func() { _ = s.server.Stop(); s.transport.closeConnections(); close(s.stopped) }() })
	select {
	case <-s.stopped:
		return nil
	case <-ctx.Done():
		s.transport.closeConnections()
		<-s.stopped
		return ctx.Err()
	}
}

type serverTransport struct {
	listener    net.Listener
	wire        *apache.TConfiguration
	limit       int
	metrics     *observability.Metrics
	mu          sync.Mutex
	connections map[*trackedConn]struct{}
	closed      bool
}

func (s *serverTransport) Listen() error { return nil }
func (s *serverTransport) Close() error {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	return s.listener.Close()
}
func (s *serverTransport) Interrupt() error { return s.Close() }
func (s *serverTransport) Accept() (apache.TTransport, error) {
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			return nil, err
		}
		s.mu.Lock()
		if s.closed {
			s.mu.Unlock()
			_ = conn.Close()
			return nil, net.ErrClosed
		}
		if len(s.connections) >= s.limit {
			s.mu.Unlock()
			_ = conn.Close()
			s.metrics.RejectedConnections.Inc()
			continue
		}
		tracked := &trackedConn{Conn: conn}
		tracked.onClose = func() { s.mu.Lock(); delete(s.connections, tracked); s.metrics.Connections.Dec(); s.mu.Unlock() }
		s.connections[tracked] = struct{}{}
		s.metrics.Connections.Inc()
		s.mu.Unlock()
		return apache.NewTSocketFromConnConf(tracked, s.wire), nil
	}
}
func (s *serverTransport) closeConnections() {
	s.mu.Lock()
	connections := make([]*trackedConn, 0, len(s.connections))
	for conn := range s.connections {
		connections = append(connections, conn)
	}
	s.mu.Unlock()
	for _, conn := range connections {
		_ = conn.Close()
	}
}

type trackedConn struct {
	net.Conn
	once    sync.Once
	onClose func()
}

func (c *trackedConn) Close() error { err := c.Conn.Close(); c.once.Do(c.onClose); return err }

type observedProcessor struct {
	apache.TProcessor
	logger  *slog.Logger
	metrics *observability.Metrics
	timeout time.Duration
}

func (p *observedProcessor) Process(ctx context.Context, in, out apache.TProtocol) (ok bool, result apache.TException) {
	name, kind, seq, err := in.ReadMessageBegin(ctx)
	if err != nil {
		return false, apache.WrapTException(err)
	}
	// Idle time is bounded by the socket, not charged to the RPC execution budget.
	ctx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()
	started := time.Now()
	method, known := p.ProcessorMap()[name]
	rpc := name
	if !known {
		rpc = "unknown"
	}
	status := "unsupported"
	defer func() {
		p.metrics.RPCs.WithLabelValues(rpc, status).Inc()
		p.metrics.RPCLatency.WithLabelValues(rpc).Observe(time.Since(started).Seconds())
		p.logger.DebugContext(ctx, "HMS request", "rpc", rpc, "status", status, "duration", time.Since(started))
	}()
	if kind == apache.CALL && known {
		ok, result = method.Process(ctx, seq, in, out)
		status = "success"
		if result != nil {
			status = "error"
		}
		if !ok {
			status = "protocol_error"
		}
		return ok, result
	}
	code := apache.UNKNOWN_METHOD
	message := "UNSUPPORTED_RPC: this read-only service does not implement the requested method"
	if kind != apache.CALL {
		code = apache.INVALID_MESSAGE_TYPE_EXCEPTION
		message = "HMS requires CALL messages"
		status = "protocol_error"
	}
	// Use Apache's codec to consume unsupported arguments and emit an application
	// exception. Check every I/O error so truncated input cannot silently succeed.
	if err := in.Skip(ctx, apache.STRUCT); err != nil {
		return false, apache.WrapTException(err)
	}
	if err := in.ReadMessageEnd(ctx); err != nil {
		return false, apache.WrapTException(err)
	}
	exception := apache.NewTApplicationException(int32(code), message)
	if err := out.WriteMessageBegin(ctx, name, apache.EXCEPTION, seq); err != nil {
		return false, apache.WrapTException(err)
	}
	if err := exception.Write(ctx, out); err != nil {
		return false, apache.WrapTException(err)
	}
	if err := out.WriteMessageEnd(ctx); err != nil {
		return false, apache.WrapTException(err)
	}
	if err := out.Flush(ctx); err != nil {
		return false, apache.WrapTException(err)
	}
	return false, exception
}
