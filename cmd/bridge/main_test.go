package main

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	pb "github.com/felinics/memoh/internal/workspace/bridgepb"
)

type blockingExecBridgeServer struct {
	pb.UnimplementedContainerServiceServer
	started chan struct{}
	release chan struct{}
}

func (s *blockingExecBridgeServer) Exec(pb.ContainerService_ExecServer) error {
	close(s.started)
	// Deliberately ignore stream cancellation. Positive-timeout bridge Exec
	// handlers can do the same while their child process is still running.
	<-s.release
	return nil
}

func TestBridgeGRPCShutdownStopsActiveStreams(t *testing.T) {
	listener, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	service := &blockingExecBridgeServer{
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	defer close(service.release)
	server := grpc.NewServer()
	pb.RegisterContainerServiceServer(server, service)
	serveDone := make(chan error, 1)
	go func() { serveDone <- server.Serve(listener) }()

	conn, err := grpc.NewClient(listener.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		server.Stop()
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = conn.Close() }()

	streamCtx, streamCancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer streamCancel()
	stream, err := pb.NewContainerServiceClient(conn).Exec(streamCtx)
	if err != nil {
		server.Stop()
		t.Fatalf("open exec stream: %v", err)
	}
	if err := stream.Send(&pb.ExecInput{Command: "sleep forever"}); err != nil {
		server.Stop()
		t.Fatalf("start exec stream: %v", err)
	}
	select {
	case <-service.started:
	case <-time.After(time.Second):
		server.Stop()
		t.Fatal("exec stream did not start")
	}

	ctx, cancel := context.WithCancel(context.Background())
	shutdownDone := make(chan struct{})
	go func() {
		stopBridgeGRPCServer(ctx, server)
		close(shutdownDone)
	}()
	cancel()

	select {
	case <-shutdownDone:
	case <-time.After(time.Second):
		server.Stop()
		t.Fatal("bridge shutdown did not finish")
	}
	if _, err := stream.Recv(); err == nil {
		t.Fatal("active exec stream survived forced bridge shutdown")
	}
	select {
	case err := <-serveDone:
		if err != nil && !errors.Is(err, grpc.ErrServerStopped) {
			t.Fatalf("serve returned %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("gRPC Serve did not return after shutdown")
	}
}

type echoExecBridgeServer struct {
	pb.UnimplementedContainerServiceServer
}

func (*echoExecBridgeServer) Exec(stream pb.ContainerService_ExecServer) error {
	for {
		input, err := stream.Recv()
		if err != nil {
			return err
		}
		if err := stream.Send(&pb.ExecOutput{Stream: pb.ExecOutput_STDOUT, Data: input.GetStdinData()}); err != nil {
			return err
		}
	}
}

func TestBridgeActiveExecSurvivesConnectionIdleWindow(t *testing.T) {
	t.Parallel()
	params := bridgeKeepaliveParameters()
	if params.MaxConnectionAge != 0 || params.MaxConnectionAgeGrace != 0 {
		t.Fatal("ACP process streams must not have a forced connection age limit")
	}
	// Compress the idle window, while using the production age/health policy.
	// A quiet but active Exec RPC must survive; cancellation must still work.
	params.MaxConnectionIdle = 50 * time.Millisecond
	listener, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer(grpc.KeepaliveParams(params))
	pb.RegisterContainerServiceServer(server, &echoExecBridgeServer{})
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)
	conn, err := grpc.NewClient(listener.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	stream, err := pb.NewContainerServiceClient(conn).Exec(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"before", "after"} {
		if err := stream.Send(&pb.ExecInput{StdinData: []byte(text), TimeoutSeconds: -1}); err != nil {
			t.Fatal(err)
		}
		output, err := stream.Recv()
		if err != nil || string(output.GetData()) != text {
			t.Fatalf("exec response = %v, %v; want %q", output, err, text)
		}
		if text == "before" {
			time.Sleep(5 * params.MaxConnectionIdle)
		}
	}
	cancel()
	if _, err := stream.Recv(); err == nil {
		t.Fatal("explicit cancellation must still close Exec")
	}
}
