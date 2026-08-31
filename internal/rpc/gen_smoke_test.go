package rpc

import (
	"testing"

	agentv1 "github.com/no-dal/ndl-ce/gen/nodal/agent/v1"
	"github.com/no-dal/ndl-ce/gen/nodal/agent/v1/agentv1connect"
)

func TestGeneratedHelloTypesExist(t *testing.T) {
	req := &agentv1.HelloRequest{}
	if req == nil {
		t.Fatal("HelloRequest")
	}
	_ = &agentv1.HelloResponse{
		FeatureFlags: []string{},
		HostPlatform: &agentv1.HostPlatform{},
	}
	_ = &agentv1.ObserveRequest{}
	_ = &agentv1.OpenSessionRequest{}
	_ = &agentv1.ExecuteRequest{
		OperationId: "phase-0",
		Method:      &agentv1.ExecuteRequest_Ping{Ping: &agentv1.Ping{}},
	}
	if agentv1connect.AgentServiceHelloProcedure == "" {
		t.Fatal("connect Hello procedure")
	}
}
