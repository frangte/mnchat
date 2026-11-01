package server

import (
	"log/slog"
	"net"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/reflection"

	searchv1 "github.com/frangte/mnchat/gen/search/v1"
)

// GRPC is the listening side of search: the API plus standard health and reflection
// services, so grpc_health_probe and grpcurl work without the proto file.
type GRPC struct {
	srv    *grpc.Server
	health *health.Server
}

func NewGRPC(svc searchv1.SearchServiceServer, m *Metrics) *GRPC {
	srv := grpc.NewServer(grpc.UnaryInterceptor(m.unaryInterceptor))
	searchv1.RegisterSearchServiceServer(srv, svc)

	hs := health.NewServer()
	hs.SetServingStatus(searchv1.SearchService_ServiceDesc.ServiceName, healthpb.HealthCheckResponse_SERVING)
	healthpb.RegisterHealthServer(srv, hs)

	reflection.Register(srv)
	return &GRPC{srv: srv, health: hs}
}

// Serve blocks until Shutdown, when it returns nil, or until the listener fails.
func (g *GRPC) Serve(lis net.Listener) error { return g.srv.Serve(lis) }

// Shutdown reports NOT_SERVING and lets running RPCs finish, then cancels whatever
// is still running after timeout.
func (g *GRPC) Shutdown(timeout time.Duration) {
	g.health.Shutdown()

	done := make(chan struct{})
	go func() {
		g.srv.GracefulStop()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(timeout):
		slog.Warn("graceful stop timed out, cancelling running requests", "timeout", timeout)
		g.srv.Stop()
		<-done
	}
}
