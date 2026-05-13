package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MoranWeissman/sharko/internal/argocd"
	"github.com/MoranWeissman/sharko/internal/gitprovider"
	"github.com/MoranWeissman/sharko/internal/models"
)

// inMemoryConnStore is a minimal config.Store implementation for dashboard
// tests — enough to satisfy DashboardService.GetStats's connSvc.List() call
// without standing up a full FileStore. Returns an empty-but-well-formed
// connections list, which is what GetStats's connection-stats branch wants
// when probing aggregate state on a fresh install.
type inMemoryConnStore struct {
	connections []models.Connection
	active      string
}

func (s *inMemoryConnStore) ListConnections() ([]models.Connection, error) {
	out := make([]models.Connection, len(s.connections))
	copy(out, s.connections)
	return out, nil
}
func (s *inMemoryConnStore) GetConnection(name string) (*models.Connection, error) {
	for i := range s.connections {
		if s.connections[i].Name == name {
			c := s.connections[i]
			return &c, nil
		}
	}
	return nil, nil
}
func (s *inMemoryConnStore) SaveConnection(conn models.Connection) error {
	s.connections = append(s.connections, conn)
	return nil
}
func (s *inMemoryConnStore) DeleteConnection(name string) error {
	out := s.connections[:0]
	for _, c := range s.connections {
		if c.Name != name {
			out = append(out, c)
		}
	}
	s.connections = out
	return nil
}
func (s *inMemoryConnStore) GetActiveConnection() (string, error) { return s.active, nil }
func (s *inMemoryConnStore) SetActiveConnection(name string) error {
	s.active = name
	return nil
}

// argocdEmptyStub returns an httptest server that answers every ArgoCD list
// endpoint with an empty `{"items":[]}` payload. Lets DashboardService.GetStats
// exercise its real ArgoCD client without dragging in a fake-client harness —
// the dashboard test is exercising the file-not-found degrade path, not the
// ArgoCD wiring itself.
func argocdEmptyStub(t *testing.T) *argocd.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"items":[]}`))
	}))
	t.Cleanup(srv.Close)
	return argocd.NewClient(srv.URL, "test-token", true)
}

// TestGetStats_MissingFileReturnsZeroState is the V124-23 / BUG-048
// regression test. When managed-clusters.yaml (or addons-catalog.yaml) is
// missing — fresh-install gitops repo, no clusters yet — GetStats MUST
// degrade to zero-state stats rather than propagate a 500 with the raw
// filesystem error string. Same isGitFileNotFound contract as
// ClusterService.ListClusters (V124-2.2).
func TestGetStats_MissingFileReturnsZeroState(t *testing.T) {
	connSvc := NewConnectionService(&inMemoryConnStore{})
	svc := NewDashboardService(connSvc, "")
	gp := &fakeGP{} // every lookup returns wrapped ErrFileNotFound

	resp, err := svc.GetStats(context.Background(), gp, argocdEmptyStub(t))
	if err != nil {
		t.Fatalf("GetStats returned err on missing-file path: %v", err)
	}
	if resp == nil {
		t.Fatal("expected non-nil response on missing-file path")
	}
	// Zero state across every category — there are no clusters, addons,
	// or applications when the gitops repo is empty.
	if resp.Clusters.Total != 0 {
		t.Errorf("expected 0 clusters, got %d", resp.Clusters.Total)
	}
	if resp.Addons.TotalAvailable != 0 {
		t.Errorf("expected 0 addons available, got %d", resp.Addons.TotalAvailable)
	}
	if resp.Addons.EnabledDeployments != 0 {
		t.Errorf("expected 0 enabled deployments, got %d", resp.Addons.EnabledDeployments)
	}
	if resp.Applications.Total != 0 {
		t.Errorf("expected 0 applications, got %d", resp.Applications.Total)
	}
}

// TestGetStats_RealErrorPropagates locks down the other half of the
// V124-23 contract: a non-file-not-found error from the git provider MUST
// propagate (5xx) rather than silently degrade to zero state. Same H2
// anti-pattern that V124-2.12 already fixed for /clusters.
func TestGetStats_RealErrorPropagates(t *testing.T) {
	cases := []struct {
		name string
		err  error
	}{
		{"github auth-or-perm error", errors.New("GitHub repository not found — check the URL and credentials")},
		{"wrong branch", errors.New("branch 'main' not found")},
		{"rate limit with 404 in body", errors.New("rate limited; body: {\"status\":404,\"reason\":\"abuse\"}")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			connSvc := NewConnectionService(&inMemoryConnStore{})
			svc := NewDashboardService(connSvc, "")
			gp := &fakeGP{
				err: map[string]error{
					"configuration/managed-clusters.yaml": tc.err,
				},
			}
			if _, err := svc.GetStats(context.Background(), gp, argocdEmptyStub(t)); err == nil {
				t.Fatalf("expected error to propagate from %q, got nil", tc.err)
			} else if !strings.Contains(err.Error(), "managed-clusters.yaml") {
				t.Errorf("expected error to mention managed-clusters.yaml, got %q", err.Error())
			}
		})
	}
}

// TestGetStats_EmptyResponseHasNoLeakedError is the over-the-wire shape
// contract for BUG-048: the missing-file path must not surface raw
// filesystem error strings to the caller. Pairs with the
// GetVersionMatrix variant in addon_test.go.
func TestGetStats_EmptyResponseHasNoLeakedError(t *testing.T) {
	connSvc := NewConnectionService(&inMemoryConnStore{})
	svc := NewDashboardService(connSvc, "")
	gp := &fakeGP{
		err: map[string]error{
			"configuration/managed-clusters.yaml": fmt.Errorf(
				"fakeGP: configuration/managed-clusters.yaml: %w",
				gitprovider.ErrFileNotFound,
			),
			"configuration/addons-catalog.yaml": fmt.Errorf(
				"fakeGP: configuration/addons-catalog.yaml: %w",
				gitprovider.ErrFileNotFound,
			),
		},
	}

	resp, err := svc.GetStats(context.Background(), gp, argocdEmptyStub(t))
	if err != nil {
		t.Fatalf("expected nil err on missing-file path, got %v", err)
	}
	body, mErr := json.Marshal(resp)
	if mErr != nil {
		t.Fatalf("response did not serialise: %v", mErr)
	}
	if strings.Contains(string(body), "managed-clusters.yaml") {
		t.Errorf("response body leaked filesystem path: %s", string(body))
	}
	if strings.Contains(string(body), "file not found") {
		t.Errorf("response body leaked error string: %s", string(body))
	}
}
