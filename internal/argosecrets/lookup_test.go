package argosecrets

import (
	"context"
	"errors"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

// argoAddedSecret builds a cluster Secret the way `argocd cluster add`
// names it: cluster-<host>-<hash>, with ArgoCD's cluster name inside.
func argoAddedSecret(k8sName, clusterName string, labels map[string]string) *corev1.Secret {
	l := map[string]string{LabelSecretType: "cluster"}
	for k, v := range labels {
		l[k] = v
	}
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: k8sName, Namespace: testNamespace, Labels: l},
		Data: map[string][]byte{
			"name":   []byte(clusterName),
			"server": []byte("https://10.0.0.5:6443"),
			"config": []byte(`{"tlsClientConfig":{"insecure":false}}`),
		},
	}
}

const argoK8sName = "cluster-10.0.0.5-3621829410"

func TestGetClusterSecret_FindsByArgoCDName(t *testing.T) {
	client := fake.NewSimpleClientset(argoAddedSecret(argoK8sName, "legacy-1", nil))
	got, err := GetClusterSecret(context.Background(), client, testNamespace, "legacy-1")
	if err != nil {
		t.Fatalf("GetClusterSecret: %v", err)
	}
	if got.Name != argoK8sName {
		t.Fatalf("found %q, want %q", got.Name, argoK8sName)
	}
}

func TestGetClusterSecret_KubernetesNameStillWorks(t *testing.T) {
	s := argoAddedSecret("legacy-1", "legacy-1", nil)
	client := fake.NewSimpleClientset(s)
	got, err := GetClusterSecret(context.Background(), client, testNamespace, "legacy-1")
	if err != nil || got.Name != "legacy-1" {
		t.Fatalf("got %v, %v", got, err)
	}
	// A Secret with no name field inside answers to its Kubernetes name.
	bare := argoAddedSecret("bare", "", nil)
	delete(bare.Data, "name")
	client = fake.NewSimpleClientset(bare)
	if got, err := GetClusterSecret(context.Background(), client, testNamespace, "bare"); err != nil || got.Name != "bare" {
		t.Fatalf("bare: got %v, %v", got, err)
	}
}

func TestGetClusterSecret_KubernetesNameWithOtherClusterNameIsNotAMatch(t *testing.T) {
	// The Secret is called legacy-1 in Kubernetes, but ArgoCD shows it as
	// "other". It is not legacy-1's connection.
	client := fake.NewSimpleClientset(argoAddedSecret("legacy-1", "other", nil))
	_, err := GetClusterSecret(context.Background(), client, testNamespace, "legacy-1")
	if !apierrors.IsNotFound(err) {
		t.Fatalf("want NotFound, got %v", err)
	}
}

func TestGetClusterSecret_NotFound(t *testing.T) {
	client := fake.NewSimpleClientset(argoAddedSecret(argoK8sName, "legacy-1", nil))
	_, err := GetClusterSecret(context.Background(), client, testNamespace, "nope")
	if !apierrors.IsNotFound(err) {
		t.Fatalf("want NotFound, got %v", err)
	}
}

func TestGetClusterSecret_TwoMatchesIsAClearError(t *testing.T) {
	client := fake.NewSimpleClientset(
		argoAddedSecret(argoK8sName, "legacy-1", nil),
		argoAddedSecret("legacy-1", "legacy-1", nil),
	)
	_, err := GetClusterSecret(context.Background(), client, testNamespace, "legacy-1")
	if !errors.Is(err, ErrAmbiguousClusterSecret) {
		t.Fatalf("want ErrAmbiguousClusterSecret, got %v", err)
	}
	want := `ArgoCD has 2 connections called "legacy-1", in the Secrets cluster-10.0.0.5-3621829410, legacy-1 in namespace "argocd". Sharko will not guess which one you mean`
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("error text:\n%s\nwant it to contain:\n%s", err.Error(), want)
	}
}

func TestGetClusterSecret_IgnoresNonClusterSecrets(t *testing.T) {
	s := argoAddedSecret("some-secret", "legacy-1", nil)
	delete(s.Labels, LabelSecretType)
	client := fake.NewSimpleClientset(s)
	if _, err := GetClusterSecret(context.Background(), client, testNamespace, "legacy-1"); !apierrors.IsNotFound(err) {
		t.Fatalf("want NotFound for a Secret that is not an ArgoCD cluster, got %v", err)
	}
}

// Every Manager path works when the Secret's Kubernetes name differs from
// the cluster name, and none of them renames or duplicates the Secret.
func TestManager_PathsWorkWithArgoCDAddedSecretNames(t *testing.T) {
	ctx := context.Background()

	t.Run("detail", func(t *testing.T) {
		client := fake.NewSimpleClientset(argoAddedSecret(argoK8sName, "legacy-1", map[string]string{"env": "dev"}))
		d, err := NewManager(client, testNamespace).GetClusterSecretDetail(ctx, "legacy-1")
		if err != nil || !d.Found || d.Labels["env"] != "dev" {
			t.Fatalf("detail = %+v, err %v", d, err)
		}
	})

	t.Run("takeover keeps the Secret name", func(t *testing.T) {
		client := fake.NewSimpleClientset(argoAddedSecret(argoK8sName, "legacy-1", map[string]string{"env": "dev"}))
		mgr := NewManager(client, testNamespace)
		res, err := mgr.TakeOverClusterSecret(ctx, "legacy-1", true, "2026-10-23T00:00:00Z", func(k string) bool { return k == "env" })
		if err != nil || !res.Found || !res.Changed {
			t.Fatalf("takeover = %+v, err %v", res, err)
		}
		live, _ := client.CoreV1().Secrets(testNamespace).Get(ctx, argoK8sName, metav1.GetOptions{})
		if live.Labels[LabelManagedBy] != ManagedByValue {
			t.Fatalf("ownership not stamped on %s: %v", argoK8sName, live.Labels)
		}
		assertOnlySecret(t, client, argoK8sName)
	})

	t.Run("annotations and labels", func(t *testing.T) {
		client := fake.NewSimpleClientset(argoAddedSecret(argoK8sName, "legacy-1", map[string]string{LabelManagedBy: ManagedByValue}))
		mgr := NewManager(client, testNamespace)
		if err := mgr.SetAnnotation(ctx, "legacy-1", AnnotationAdopted, "true"); err != nil {
			t.Fatal(err)
		}
		if v, err := mgr.GetAnnotation(ctx, "legacy-1", AnnotationAdopted); err != nil || v != "true" {
			t.Fatalf("GetAnnotation = %q, %v", v, err)
		}
		if v, err := mgr.GetManagedByLabel(ctx, "legacy-1"); err != nil || v != ManagedByValue {
			t.Fatalf("GetManagedByLabel = %q, %v", v, err)
		}
		if err := mgr.Unadopt(ctx, "legacy-1"); err != nil {
			t.Fatal(err)
		}
		live, _ := client.CoreV1().Secrets(testNamespace).Get(ctx, argoK8sName, metav1.GetOptions{})
		if _, ok := live.Labels[LabelManagedBy]; ok {
			t.Fatalf("unadopt left the ownership label: %v", live.Labels)
		}
		assertOnlySecret(t, client, argoK8sName)
	})

	t.Run("strip ownership label", func(t *testing.T) {
		client := fake.NewSimpleClientset(argoAddedSecret(argoK8sName, "legacy-1", map[string]string{LabelManagedBy: ManagedByValue}))
		stripped, err := NewManager(client, testNamespace).StripOwnershipLabel(ctx, "legacy-1")
		if err != nil || !stripped {
			t.Fatalf("strip = %v, %v", stripped, err)
		}
	})

	t.Run("delete removes the real Secret", func(t *testing.T) {
		client := fake.NewSimpleClientset(argoAddedSecret(argoK8sName, "legacy-1", map[string]string{LabelManagedBy: ManagedByValue}))
		if err := NewManager(client, testNamespace).Delete(ctx, "legacy-1"); err != nil {
			t.Fatal(err)
		}
		if _, err := client.CoreV1().Secrets(testNamespace).Get(ctx, argoK8sName, metav1.GetOptions{}); !apierrors.IsNotFound(err) {
			t.Fatalf("Secret %s still there after Delete: %v", argoK8sName, err)
		}
	})

	t.Run("drop labels", func(t *testing.T) {
		s := argoAddedSecret(argoK8sName, "legacy-1", map[string]string{LabelManagedBy: ManagedByValue, "env": "dev"})
		s.Annotations = map[string]string{AnnotationTakeoverPreservedLabels: "env"}
		client := fake.NewSimpleClientset(s)
		removed, found, err := NewManager(client, testNamespace).DropLabels(ctx, "legacy-1", []string{"env"})
		if err != nil || !found || len(removed) != 1 {
			t.Fatalf("drop = %v %v %v", removed, found, err)
		}
	})

	t.Run("ensure updates the existing Secret instead of making a second one", func(t *testing.T) {
		client := fake.NewSimpleClientset(argoAddedSecret(argoK8sName, "legacy-1", map[string]string{LabelManagedBy: ManagedByValue}))
		spec := baseSpec()
		spec.Name = "legacy-1"
		if _, err := NewManager(client, testNamespace).Ensure(ctx, spec); err != nil {
			t.Fatal(err)
		}
		assertOnlySecret(t, client, argoK8sName)
	})

	t.Run("label sync", func(t *testing.T) {
		client := fake.NewSimpleClientset(argoAddedSecret(argoK8sName, "legacy-1", nil))
		_, found, err := NewManager(client, testNamespace).SyncLabelsOnly(ctx, "legacy-1", map[string]string{"addons.sharko.dev/x": "enabled"})
		if err != nil || !found {
			t.Fatalf("SyncLabelsOnly found=%v err=%v", found, err)
		}
	})
}

func assertOnlySecret(t *testing.T, client *fake.Clientset, want string) {
	t.Helper()
	list, err := client.CoreV1().Secrets(testNamespace).List(context.Background(), metav1.ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Items) != 1 || list.Items[0].Name != want {
		names := []string{}
		for _, s := range list.Items {
			names = append(names, s.Name)
		}
		t.Fatalf("Secrets in namespace = %v, want only %q (the user's Secret must not be renamed or copied)", names, want)
	}
}

func TestManager_ClusterSecretNames(t *testing.T) {
	notCluster := argoAddedSecret("not-a-cluster", "other", nil)
	delete(notCluster.Labels, LabelSecretType)
	client := fake.NewSimpleClientset(
		argoAddedSecret(argoK8sName, "legacy-1", nil),
		argoAddedSecret("legacy-2", "legacy-2", nil),
		// Two Secrets with the same cluster name: Sharko will not guess.
		argoAddedSecret("twin-a", "twin", nil),
		argoAddedSecret("twin-b", "twin", nil),
		notCluster,
	)
	got, err := NewManager(client, testNamespace).ClusterSecretNames(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"legacy-1": argoK8sName, "legacy-2": "legacy-2"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

// R13 (m5): a repository Secret that happens to have the cluster's
// Kubernetes name is not that cluster's connection.
func TestGetClusterSecret_SkipsATypedNonClusterSecret(t *testing.T) {
	repo := argoAddedSecret("prod-eu", "", map[string]string{LabelSecretType: "repository"})
	delete(repo.Data, "name")
	client := fake.NewSimpleClientset(repo)
	_, err := GetClusterSecret(context.Background(), client, testNamespace, "prod-eu")
	if !apierrors.IsNotFound(err) {
		t.Fatalf("a repository Secret named prod-eu was taken as the cluster's connection; err = %v", err)
	}

	// A cluster Secret whose labels were all wiped is still found, so the
	// reconciler can see it and put its labels back.
	wiped := argoAddedSecret("prod-us", "", nil)
	delete(wiped.Data, "name")
	wiped.Labels = nil
	client = fake.NewSimpleClientset(wiped)
	if got, err := GetClusterSecret(context.Background(), client, testNamespace, "prod-us"); err != nil || got.Name != "prod-us" {
		t.Fatalf("a cluster Secret with no labels left was not found: %v, %v", got, err)
	}
}
