package agentcontrol

import (
	"context"
	"net"
	"testing"

	control "github.com/yyhuni/lunafox/contracts/gen/lunafox/agent/control/v1"
	planeauth "github.com/yyhuni/lunafox/server/internal/grpc/planeauth"
	agentdomain "github.com/yyhuni/lunafox/server/internal/modules/agent/domain"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
)

func TestLocalNativeAgentRequiresConfiguredIdentityAndLoopback(t *testing.T) {
	service := (&ControlPlaneService{}).WithLocalNativeAgent("local-instance")
	makeContext := func(ip string, marker ...string) context.Context {
		ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs(marker...))
		return peer.NewContext(ctx, &peer.Peer{Addr: &net.TCPAddr{IP: net.ParseIP(ip), Port: 1234}})
	}
	for _, test := range []struct {
		name, instance, ip string
		marker             []string
		want               bool
	}{
		{"matching loopback", "local-instance", "127.0.0.1", []string{NativeExecutionMetadataKey, "1"}, true},
		{"matching ipv6 loopback", "local-instance", "::1", []string{NativeExecutionMetadataKey, "1"}, true},
		{"other identity", "other", "127.0.0.1", []string{NativeExecutionMetadataKey, "1"}, false},
		{"remote peer", "local-instance", "192.0.2.10", []string{NativeExecutionMetadataKey, "1"}, false},
		{"missing marker", "local-instance", "127.0.0.1", nil, false},
		{"duplicate marker", "local-instance", "127.0.0.1", []string{NativeExecutionMetadataKey, "1", NativeExecutionMetadataKey, "1"}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := service.isLocalNativeAgent(makeContext(test.ip, test.marker...), test.instance); got != test.want {
				t.Fatalf("local native admission = %v, want %v", got, test.want)
			}
		})
	}
}

func TestNativeAgentCanClaimOnlyWhenLocallyAuthorized(t *testing.T) {
	for _, test := range []struct {
		name, address string
		configured    bool
		wantClaims    int
	}{
		{"configured loopback", "127.0.0.1", true, 1},
		{"remote peer", "192.0.2.1", true, 0},
		{"local mode disabled", "127.0.0.1", false, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			agent := &agentdomain.Agent{ID: 72, InstanceID: "agt-72"}
			claims := &executionClaimRuntimeStub{}
			service := NewControlPlaneService(&agentFinderStub{agent: agent}, &runtimeLifecycleStub{}, claims)
			if test.configured {
				service.WithLocalNativeAgent(agent.InstanceID)
			}
			ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs(planeauth.AgentAuthenticationTokenMetadataKey, "agent-token-72", NativeExecutionMetadataKey, "1"))
			ctx = peer.NewContext(ctx, &peer.Peer{Addr: &net.TCPAddr{IP: net.ParseIP(test.address), Port: 1234}})
			registration := runtimeRegisterSessionRequest("agt-72", "session-72")
			registration.GetRegisterSession().ContainerRuntimeReady = false
			stream := &fakeConnectStream{ctx: ctx, incoming: []*control.ConnectRequest{registration,
				{Payload: &control.ConnectRequest_RequestTask{RequestTask: &control.RequestTask{RequestId: "550e8400-e29b-41d4-a716-446655440000"}}}}}
			if err := service.Connect(stream); err != nil {
				t.Fatal(err)
			}
			if claims.calls != test.wantClaims {
				t.Fatalf("claims=%d, want %d", claims.calls, test.wantClaims)
			}
			if test.wantClaims == 1 && (!claims.snapshot.NativeRuntimeReady || claims.snapshot.ContainerRuntimeReady) {
				t.Fatalf("incorrect runtime capability: %#v", claims.snapshot)
			}
		})
	}
}
