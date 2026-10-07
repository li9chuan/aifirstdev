package business

import (
	"testing"

	"github.com/li9chuan/aifirstdev/aifd-examples/desktool/internal/services/session"
)

func TestBootstrapReturnsPublicData(t *testing.T) {
	result := NewService().Bootstrap(session.State{}, "")
	if result.Title == "" || result.Summary == "" {
		t.Fatalf("Bootstrap = %+v, want title and summary", result)
	}
	if len(result.Nav) == 0 || len(result.Activity) == 0 {
		t.Fatalf("Bootstrap = %+v, want nav and activity", result)
	}
}

func TestWorkspaceRequiresAuthenticatedSession(t *testing.T) {
	result := NewService().Workspace(session.State{}, "")
	if result.Session.Authenticated {
		t.Fatal("Workspace returned authenticated session")
	}
	if result.Message == "" {
		t.Fatal("Workspace message is empty")
	}
}

func TestWorkspaceReturnsSampleDataForAuthenticatedSession(t *testing.T) {
	result := NewService().Workspace(session.State{
		Authenticated: true,
		DisplayName:   "Demo User",
		Role:          "Workspace Admin",
	}, "")
	if len(result.Metrics) == 0 || len(result.Tasks) == 0 {
		t.Fatalf("Workspace = %+v, want metrics and tasks", result)
	}
}
