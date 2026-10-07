package runtime

import (
	"testing"

	base "github.com/li9chuan/aifirstdev/aifd-contracts"
	rpc "github.com/li9chuan/aifirstdev/aifd-rpc/contracts"
	rpcrt "github.com/li9chuan/aifirstdev/aifd-rpc/runtime"
	schema "github.com/li9chuan/aifirstdev/aifd-schema/contracts"
)

func TestRegistryRejectsDuplicateNameOrID(t *testing.T) {
	registry := NewRegistry()
	if err := registry.Register(schema.MessageDescriptor{Name: "MceInventoryInfo", ID: 1001}); err != nil {
		t.Fatalf("Register error = %v", err)
	}
	if err := registry.Register(schema.MessageDescriptor{Name: "MceInventoryInfo", ID: 1002}); err == nil {
		t.Fatal("duplicate name register error = nil")
	}
	if err := registry.Register(schema.MessageDescriptor{Name: "MceOther", ID: 1001}); err == nil {
		t.Fatal("duplicate id register error = nil")
	}
	if _, ok := registry.GetByName("MceInventoryInfo"); !ok {
		t.Fatal("GetByName did not find registered message")
	}
	if _, ok := registry.GetByID(1001); !ok {
		t.Fatal("GetByID did not find registered message")
	}
}

func TestCheckCompatibilityDetectsBreakingChanges(t *testing.T) {
	previous := []schema.MessageDescriptor{{
		Name:      "MceInventoryInfo",
		ID:        1001,
		Direction: schema.DirectionClientToServer,
		Fields: []schema.FieldDescriptor{
			{Number: 1, Name: "uid", Type: "int64", Optional: true},
			{Number: 2, Name: "page", Type: "int32", Optional: true},
		},
	}}
	current := []schema.MessageDescriptor{{
		Name:      "MceInventoryInfo",
		ID:        1001,
		Direction: schema.DirectionServerToServer,
		Fields: []schema.FieldDescriptor{
			{Number: 1, Name: "uid", Type: "string", Optional: true},
			{Number: 2, Name: "page_no", Type: "int32", Optional: true},
			{Number: 3, Name: "limit", Type: "int32"},
		},
	}}

	report := CheckCompatibility(previous, current)
	if !report.HasBreaking() {
		t.Fatal("report.HasBreaking() = false, want true")
	}
	if len(report.Changes) != 4 {
		t.Fatalf("change count = %d, want 4: %+v", len(report.Changes), report.Changes)
	}
}

func TestBindRoutesRegistersInboundMessages(t *testing.T) {
	schemas := NewRegistry()
	if err := schemas.Register(schema.MessageDescriptor{Name: "MceInventoryInfo", Direction: schema.DirectionClientToServer}); err != nil {
		t.Fatalf("Register client message error = %v", err)
	}
	if err := schemas.Register(schema.MessageDescriptor{Name: "MseInventoryInfo", Direction: schema.DirectionServerToClient}); err != nil {
		t.Fatalf("Register server message error = %v", err)
	}
	router := rpcrt.NewRouter()

	err := BindRoutes(schemas, router, BindOptions{
		Service: base.ServiceRole("game"),
		HandlerFactory: func(schema.MessageDescriptor) rpc.Handler {
			return NoopHandler
		},
	})
	if err != nil {
		t.Fatalf("BindRoutes error = %v", err)
	}
	routes := router.Routes()
	if len(routes) != 1 {
		t.Fatalf("route count = %d, want 1", len(routes))
	}
	if routes[0].Name != "MceInventoryInfo" {
		t.Fatalf("route name = %s, want MceInventoryInfo", routes[0].Name)
	}
}
