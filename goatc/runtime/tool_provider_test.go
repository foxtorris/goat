package runtime

import (
	"context"
	"io/fs"
	"net"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
	"github.com/torrischen/goat/agent/contextmgr/ram"
	"github.com/torrischen/goat/agent/react"
	pluginpb "github.com/torrischen/goat/agent/toolplugin/pb"
	"github.com/torrischen/goat/goatc/config"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/emptypb"
)

type testPluginService struct {
	pluginpb.UnimplementedPluginServiceServer
	name      string
	initCalls atomic.Int32
	pingCalls atomic.Int32
}

func (s *testPluginService) Init(context.Context, *emptypb.Empty) (*emptypb.Empty, error) {
	s.initCalls.Add(1)
	return &emptypb.Empty{}, nil
}

func (s *testPluginService) Name(context.Context, *emptypb.Empty) (*pluginpb.NameResponse, error) {
	return &pluginpb.NameResponse{Name: s.name}, nil
}

func (s *testPluginService) Description(context.Context, *emptypb.Empty) (*pluginpb.DescriptionResponse, error) {
	return &pluginpb.DescriptionResponse{Description: "test tool"}, nil
}

func (s *testPluginService) Properties(context.Context, *emptypb.Empty) (*pluginpb.PropertiesResponse, error) {
	return &pluginpb.PropertiesResponse{}, nil
}

func (s *testPluginService) Ping(context.Context, *emptypb.Empty) (*emptypb.Empty, error) {
	s.pingCalls.Add(1)
	return &emptypb.Empty{}, nil
}

func TestLoadToolProvidersLoadsMultipleGRPCAndMCPServers(t *testing.T) {
	grpcAddressOne, grpcServiceOne := startTestPluginServer(t, "grpc_one")
	grpcAddressTwo, grpcServiceTwo := startTestPluginServer(t, "grpc_two")
	mcpServerOne := startTestMCPServer(t, "mcp_one")
	mcpServerTwo := startTestMCPServer(t, "mcp_two")

	cfg := &config.Config{
		Agent: config.Agent{Name: "provider-test"},
		Tools: []config.Tool{
			{Provider: config.ToolProviderGRPC, Address: grpcAddressOne},
			{Provider: config.ToolProviderGRPC, Address: grpcAddressTwo},
			{Provider: config.ToolProviderMCP, Transport: config.MCPTransportStreamableHTTP, URL: mcpServerOne.URL},
			{Provider: config.ToolProviderMCP, Transport: config.MCPTransportStreamableHTTP, URL: mcpServerTwo.URL},
		},
	}
	agent := react.NewAgent(nil, 128, ram.NewRAMContextManager())
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	resources, err := loadToolProviders(ctx, agent, cfg, emptyFS{})
	if err != nil {
		t.Fatalf("loadToolProviders() error = %v", err)
	}
	defer closeResources(resources)

	if len(resources) != 2 {
		t.Fatalf("len(resources) = %d, want 2 MCP clients", len(resources))
	}
	for _, service := range []*testPluginService{grpcServiceOne, grpcServiceTwo} {
		if service.initCalls.Load() != 1 {
			t.Errorf("gRPC Init calls = %d, want 1", service.initCalls.Load())
		}
		if service.pingCalls.Load() != 1 {
			t.Errorf("gRPC Ping calls = %d, want 1", service.pingCalls.Load())
		}
	}
}

func TestExpandValuesUsesRuntimeEnvironment(t *testing.T) {
	t.Setenv("GOATC_TEST_TOKEN", "secret")
	got := expandValues(map[string]string{
		"Authorization": "Bearer ${GOATC_TEST_TOKEN}",
	})
	if got["Authorization"] != "Bearer secret" {
		t.Fatalf("Authorization = %q, want %q", got["Authorization"], "Bearer secret")
	}
}

func startTestPluginServer(t *testing.T, name string) (string, *testPluginService) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	server := grpc.NewServer()
	service := &testPluginService{name: name}
	pluginpb.RegisterPluginServiceServer(server, service)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() {
		server.Stop()
		_ = listener.Close()
	})
	return listener.Addr().String(), service
}

func startTestMCPServer(t *testing.T, toolName string) *httptest.Server {
	t.Helper()
	server := mcpserver.NewMCPServer("test-server", "1.0.0", mcpserver.WithToolCapabilities(false))
	server.AddTool(
		mcp.NewTool(toolName, mcp.WithDescription("test tool")),
		func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return mcp.NewToolResultText("ok"), nil
		},
	)
	httpServer := httptest.NewServer(mcpserver.NewStreamableHTTPServer(server))
	t.Cleanup(httpServer.Close)
	return httpServer
}

type emptyFS struct{}

func (emptyFS) Open(string) (fs.File, error) {
	return nil, fs.ErrNotExist
}
