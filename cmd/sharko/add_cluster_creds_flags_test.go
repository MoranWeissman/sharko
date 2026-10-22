package main

// add_cluster_creds_flags_test.go — ep3 fix 2, 16 and 20. `sharko add-cluster`
// can say how Sharko gets the cluster's credentials, `add-clusters
// --from-provider` needs no dummy name, and `unadopt-cluster` names the real
// annotation.

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// runAddClusterCapturingBody runs add-cluster with the given flags against a
// test server and returns the JSON body the command sent.
func runAddClusterCapturingBody(t *testing.T, flags map[string]string) (map[string]any, error) {
	t.Helper()
	var got map[string]any
	startCLITestServer(t, func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &got)
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "success", "cluster": map[string]any{"name": "prod-eu"}})
	})
	resetFlags(addClusterCmd)
	setFlags(t, addClusterCmd, flags)
	_, err := captureStdoutT(t, func() error {
		return addClusterCmd.RunE(addClusterCmd, []string{"prod-eu"})
	})
	return got, err
}

func TestAddCluster_EKSTokenFlagsReachTheRequestBody(t *testing.T) {
	got, err := runAddClusterCapturingBody(t, map[string]string{
		"creds-source": "eks-token",
		"region":       "eu-west-1",
		"role-arn":     "arn:aws:iam::123456789012:role/sharko-access",
		"secret-path":  "clusters/prod-eu",
	})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"name": "prod-eu", "addons": map[string]any{}, "region": "eu-west-1",
		"creds_source": "eks-token", "provider": "eks",
		"role_arn": "arn:aws:iam::123456789012:role/sharko-access", "secret_path": "clusters/prod-eu",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("body = %v\nwant %v", got, want)
	}
}

func TestAddCluster_SecretKubeconfigFlagsReachTheRequestBody(t *testing.T) {
	got, err := runAddClusterCapturingBody(t, map[string]string{
		"creds-source": "secret-kubeconfig",
		"secret-path":  "clusters/prod-eu",
		"region":       "eu-west-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"name": "prod-eu", "addons": map[string]any{}, "region": "eu-west-1",
		"creds_source": "secret-kubeconfig", "provider": "eks", "secret_path": "clusters/prod-eu",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("body = %v\nwant %v", got, want)
	}
}

func TestAddCluster_InlineKubeconfigFileIsSentAsKubeconfig(t *testing.T) {
	f := filepath.Join(t.TempDir(), "kc.yaml")
	if err := os.WriteFile(f, []byte("apiVersion: v1\nkind: Config\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := runAddClusterCapturingBody(t, map[string]string{
		"creds-source": "inline-kubeconfig", "kubeconfig-file": f,
	})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"name": "prod-eu", "addons": map[string]any{}, "region": "",
		"creds_source": "inline-kubeconfig", "provider": "kubeconfig",
		"kubeconfig": "apiVersion: v1\nkind: Config\n",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("body = %v\nwant %v", got, want)
	}
}

func TestAddCluster_ConnectionManagedByUserIsSent_SharkoIsNot(t *testing.T) {
	got, err := runAddClusterCapturingBody(t, map[string]string{"creds-source": "eks-token", "connection-managed-by": "user"})
	if err != nil {
		t.Fatal(err)
	}
	if got["connection_managed_by"] != "user" {
		t.Errorf("connection_managed_by = %v, want user", got["connection_managed_by"])
	}
	got, err = runAddClusterCapturingBody(t, map[string]string{"creds-source": "eks-token", "connection-managed-by": "sharko"})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := got["connection_managed_by"]; ok {
		t.Errorf("the default owner must not be sent, got %v", got["connection_managed_by"])
	}
}

func TestAddCluster_NoNewFlagsKeepsTheOldBody(t *testing.T) {
	got, err := runAddClusterCapturingBody(t, map[string]string{"region": "us-east-1"})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"name": "prod-eu", "addons": map[string]any{}, "region": "us-east-1"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("body = %v\nwant %v", got, want)
	}
}

func TestAddCluster_WrongFlagMixIsRefusedBeforeAnythingIsSent(t *testing.T) {
	f := filepath.Join(t.TempDir(), "kc.yaml")
	_ = os.WriteFile(f, []byte("x: y\n"), 0o600)
	cases := []struct {
		name  string
		flags map[string]string
		want  string
	}{
		{"unknown source", map[string]string{"creds-source": "magic"}, `unknown --creds-source "magic" (use eks-token, secret-kubeconfig or inline-kubeconfig)`},
		{"unknown owner", map[string]string{"connection-managed-by": "bob"}, `unknown --connection-managed-by "bob" (use "sharko" or "user")`},
		{"inline with secret path", map[string]string{"creds-source": "inline-kubeconfig", "secret-path": "a"}, "--secret-path cannot be used with --creds-source inline-kubeconfig"},
		{"inline with region", map[string]string{"creds-source": "inline-kubeconfig", "region": "r"}, "--region cannot be used with --creds-source inline-kubeconfig"},
		{"inline with role", map[string]string{"creds-source": "inline-kubeconfig", "role-arn": "r"}, "--role-arn cannot be used with --creds-source inline-kubeconfig"},
		{"file with eks", map[string]string{"creds-source": "eks-token", "kubeconfig-file": f}, "--kubeconfig-file can only be used with --creds-source inline-kubeconfig"},
		{"file with no source", map[string]string{"kubeconfig-file": f}, "--kubeconfig-file needs --creds-source inline-kubeconfig"},
		{"role with secret kubeconfig", map[string]string{"creds-source": "secret-kubeconfig", "role-arn": "r"}, "--role-arn can only be used with --creds-source eks-token"},
		{"missing file", map[string]string{"creds-source": "inline-kubeconfig", "kubeconfig-file": "/nonexistent/kc"}, "could not read the kubeconfig file"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			called := false
			startCLITestServer(t, func(w http.ResponseWriter, r *http.Request) { called = true })
			resetFlags(addClusterCmd)
			setFlags(t, addClusterCmd, c.flags)
			_, err := captureStdoutT(t, func() error {
				return addClusterCmd.RunE(addClusterCmd, []string{"prod-eu"})
			})
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("error = %v, want it to contain %q", err, c.want)
			}
			if called {
				t.Error("a request was sent although the flags were refused")
			}
		})
	}
}

func TestAddCluster_HelpGivesOneExamplePerSource(t *testing.T) {
	for _, want := range []string{
		"sharko add-cluster prod-eu --creds-source eks-token --region eu-west-1 --role-arn",
		"sharko add-cluster prod-eu --creds-source secret-kubeconfig --secret-path clusters/prod-eu",
		"sharko add-cluster prod-eu --creds-source inline-kubeconfig --kubeconfig-file ./prod-eu.kubeconfig",
		"sharko add-cluster prod-eu --creds-source eks-token --connection-managed-by user",
	} {
		if !strings.Contains(addClusterCmd.Long, want) {
			t.Errorf("help is missing the example %q", want)
		}
	}
}

// Fix 16: --from-provider needs no name; the old form still parses.
func TestAddClusters_FromProviderNeedsNoName(t *testing.T) {
	startCLITestServer(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"clusters": []map[string]any{{"name": "a", "registered": true}}})
	})
	for _, args := range [][]string{{}, {"ignored"}} {
		resetFlags(addClustersCmd)
		setFlags(t, addClustersCmd, map[string]string{"from-provider": "true"})
		if err := addClustersCmd.Args(addClustersCmd, args); err != nil {
			t.Errorf("args %v refused: %v", args, err)
		}
		out, err := captureStdoutT(t, func() error { return addClustersCmd.RunE(addClustersCmd, args) })
		if err != nil || !strings.Contains(out, "All provider clusters are already registered.") {
			t.Errorf("args %v: err=%v out=%q", args, err, out)
		}
	}
}

func TestAddClusters_WithoutFromProviderStillNeedsNames(t *testing.T) {
	startCLITestServer(t, func(w http.ResponseWriter, r *http.Request) {})
	resetFlags(addClustersCmd)
	_, err := captureStdoutT(t, func() error { return addClustersCmd.RunE(addClustersCmd, nil) })
	want := "give the cluster names as a comma-separated list (for example: sharko add-clusters prod-eu,prod-us), or use --from-provider"
	if err == nil || err.Error() != want {
		t.Errorf("error = %v, want %q", err, want)
	}
}

func TestAddClusters_HelpShowsTheNameIsOptional(t *testing.T) {
	if addClustersCmd.Use != "add-clusters [cluster1,cluster2,...]" {
		t.Errorf("Use = %q", addClustersCmd.Use)
	}
	if !strings.Contains(addClustersCmd.Long, "sharko add-clusters --from-provider") {
		t.Error("help has no example of --from-provider on its own")
	}
}

// Fix 20: the help names the annotation Sharko really writes, and not the old one.
func TestUnadoptHelp_NamesTheRealAnnotation(t *testing.T) {
	if !strings.Contains(unadoptClusterCmd.Long, "sharko.dev/adopted annotation") {
		t.Errorf("help does not name sharko.dev/adopted:\n%s", unadoptClusterCmd.Long)
	}
	if strings.Contains(unadoptClusterCmd.Long, "sharko.sharko.dev") {
		t.Error("help still names the old doubled-prefix annotation")
	}
}
