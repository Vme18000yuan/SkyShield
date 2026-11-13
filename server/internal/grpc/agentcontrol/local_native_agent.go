package agentcontrol

import (
	"context"
	"net"
	"strings"

	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
)

// NativeExecutionMetadataKey marks the separately configured local executor.
// The marker alone does not grant execution: identity and loopback are checked.
const NativeExecutionMetadataKey = "skyshield-native-executor"

type localNativeAgentContextKey struct{}

func (s *ControlPlaneService) WithLocalNativeAgent(instanceID string) *ControlPlaneService {
	if s != nil {
		s.localNativeAgentID = strings.TrimSpace(instanceID)
	}
	return s
}

func (s *ControlPlaneService) isLocalNativeAgent(ctx context.Context, instanceID string) bool {
	if s == nil || s.localNativeAgentID == "" || instanceID != s.localNativeAgentID {
		return false
	}
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok || len(md.Get(NativeExecutionMetadataKey)) != 1 || md.Get(NativeExecutionMetadataKey)[0] != "1" {
		return false
	}
	p, ok := peer.FromContext(ctx)
	if !ok || p.Addr == nil {
		return false
	}
	host, _, err := net.SplitHostPort(p.Addr.String())
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func localNativeAgentFromContext(ctx context.Context) bool {
	value, _ := ctx.Value(localNativeAgentContextKey{}).(bool)
	return value
}
