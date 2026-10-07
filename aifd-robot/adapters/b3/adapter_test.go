package b3

import (
	"context"
	"strings"
	"testing"

	"github.com/li9chuan/aifirstdev/aifd-robot/behavior"
	robot "github.com/li9chuan/aifirstdev/aifd-robot/contracts"
	"google.golang.org/protobuf/proto"
)

type fakeAgent struct {
	id  string
	uid int64
}

func (a fakeAgent) ID() string {
	return a.id
}

func (a fakeAgent) UID() int64 {
	return a.uid
}

func (a fakeAgent) Account() string {
	return "account"
}

func (a fakeAgent) IsLoggedIn() bool {
	return true
}

func (a fakeAgent) Call(proto.Message, ...any) error {
	return nil
}

func (a fakeAgent) On(any) error {
	return nil
}

func TestTreeStoreAttachBuildsPerAgentRoot(t *testing.T) {
	registry := behavior.NewRegistry()
	called := 0
	if err := registry.Register(robot.ActionRegistration{
		Name: "login",
		Factory: func(robot.Settings) robot.Action {
			return robot.ActionFunc(func(context.Context, robot.Agent) robot.Status {
				called++
				return robot.StatusSuccess
			})
		},
	}); err != nil {
		t.Fatalf("Register error = %v", err)
	}
	project := Project{Trees: []TreeSpec{{
		ID:    "root-tree",
		Title: "root",
		Root: robot.BehaviorSpec{
			Type: "sequence",
			Children: []robot.BehaviorSpec{
				{Name: "login"},
			},
		},
	}}}
	store := NewTreeStore()
	agent := fakeAgent{id: "agent-1", uid: 1001}
	if err := store.Attach(agent, project, registry); err != nil {
		t.Fatalf("Attach error = %v", err)
	}
	root, ok := store.Root(agent.ID())
	if !ok {
		t.Fatal("Root ok = false, want true")
	}
	if status := root.Tick(context.Background(), agent); status != robot.StatusSuccess {
		t.Fatalf("root status = %s, want success", status)
	}
	if called != 1 {
		t.Fatalf("action called = %d, want 1", called)
	}
}

func TestTreeStoreAttachRequiresRoot(t *testing.T) {
	registry := behavior.NewRegistry()
	store := NewTreeStore()
	agent := fakeAgent{id: "agent-1", uid: 1001}
	project := Project{Trees: []TreeSpec{{ID: "tree", Root: robot.BehaviorSpec{Type: "sequence"}}}}

	err := store.Attach(agent, project, registry)
	if err == nil || !strings.Contains(err.Error(), "root tree is missing") {
		t.Fatalf("Attach error = %v, want missing root", err)
	}
}

func TestTreeStoreAttachRejectsDuplicateRoot(t *testing.T) {
	registry := behavior.NewRegistry()
	store := NewTreeStore()
	agent := fakeAgent{id: "agent-1", uid: 1001}
	project := Project{Trees: []TreeSpec{
		{ID: "root-a", Title: "root", Root: robot.BehaviorSpec{Type: "sequence"}},
		{ID: "root-b", Title: "root", Root: robot.BehaviorSpec{Type: "sequence"}},
	}}

	err := store.Attach(agent, project, registry)
	if err == nil || !strings.Contains(err.Error(), "root tree is duplicated") {
		t.Fatalf("Attach error = %v, want duplicate root", err)
	}
}

func TestLoaderFunc(t *testing.T) {
	loader := LoaderFunc(func(_ context.Context, path string) (Project, error) {
		if path != "demo.b3" {
			t.Fatalf("path = %s, want demo.b3", path)
		}
		return Project{Trees: []TreeSpec{{ID: "root", Title: "root"}}}, nil
	})
	project, err := loader.Load(context.Background(), "demo.b3")
	if err != nil {
		t.Fatalf("Load error = %v", err)
	}
	if len(project.Trees) != 1 || project.Trees[0].ID != "root" {
		t.Fatalf("project = %+v, want root tree", project)
	}
}
