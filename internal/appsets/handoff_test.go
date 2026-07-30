package appsets

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
)

// v3ScaffoldAppSetObject is the ApplicationSet
// templates/bootstrap/templates/addons-appset.yaml actually renders, cut
// down to the fields this package touches. The two things that make it
// dangerous are both here: no preserveResourcesOnDeletion, and the ArgoCD
// resources finalizer stamped onto the Application template.
func v3ScaffoldAppSetObject(name string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "argoproj.io/v1alpha1",
		"kind":       "ApplicationSet",
		"metadata": map[string]interface{}{
			"name":      name,
			"namespace": "argocd",
		},
		"spec": map[string]interface{}{
			"generators": []interface{}{
				map[string]interface{}{
					"matrix": map[string]interface{}{
						"generators": []interface{}{
							map[string]interface{}{
								"clusters": map[string]interface{}{
									"selector": map[string]interface{}{
										"matchLabels": map[string]interface{}{
											"argocd.argoproj.io/secret-type": "cluster",
											name:                             "enabled",
										},
									},
								},
							},
						},
					},
				},
			},
			"template": map[string]interface{}{
				"metadata": map[string]interface{}{
					"name":       name + "-{{ .name }}",
					"finalizers": []interface{}{ResourcesFinalizer},
				},
			},
		},
	}}
}

func generatedApp(name, owner string, withFinalizer bool) *unstructured.Unstructured {
	finalizers := []interface{}{}
	if withFinalizer {
		finalizers = append(finalizers, ResourcesFinalizer)
	}
	return &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "argoproj.io/v1alpha1",
		"kind":       "Application",
		"metadata": map[string]interface{}{
			"name":       name,
			"namespace":  "argocd",
			"finalizers": finalizers,
			"ownerReferences": []interface{}{
				map[string]interface{}{
					"apiVersion": "argoproj.io/v1alpha1",
					"kind":       "ApplicationSet",
					"name":       owner,
				},
			},
		},
	}}
}

func newTestClient(objs ...runtime.Object) *DynamicReader {
	scheme := runtime.NewScheme()
	scheme.AddKnownTypeWithName(
		GroupVersionResource.GroupVersion().WithKind("ApplicationSetList"), &unstructured.UnstructuredList{})
	scheme.AddKnownTypeWithName(
		ApplicationsGroupVersionResource.GroupVersion().WithKind("ApplicationList"), &unstructured.UnstructuredList{})
	client := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, map[schema.GroupVersionResource]string{
		GroupVersionResource:             "ApplicationSetList",
		ApplicationsGroupVersionResource: "ApplicationList",
	}, objs...)
	return NewDynamicReader(client, "argocd")
}

// TestPreserve_TurnsOffBothWaysAnAppSetCanDelete is the pinning test for
// the pre-merge half of the runtime handoff. After it, the ApplicationSet
// must be deletion-safe AND its template must no longer stamp the ArgoCD
// resources finalizer on anything it generates.
func TestPreserve_TurnsOffBothWaysAnAppSetCanDelete(t *testing.T) {
	r := newTestClient(v3ScaffoldAppSetObject("cert-manager"))

	// Sanity: the fixture starts out dangerous, or the test proves nothing.
	before, err := r.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(before) != 1 || before[0].DeletionSafe() {
		t.Fatalf("fixture should start NOT deletion-safe, got %+v", before)
	}

	if err := r.Preserve(context.Background(), "cert-manager"); err != nil {
		t.Fatalf("Preserve: %v", err)
	}

	after, err := r.List(context.Background())
	if err != nil {
		t.Fatalf("List after Preserve: %v", err)
	}
	if !after[0].DeletionSafe() {
		t.Error("ApplicationSet is still not deletion-safe after Preserve — " +
			"losing its generators would delete the workloads it installed")
	}

	obj, err := r.client.Resource(GroupVersionResource).Namespace("argocd").
		Get(context.Background(), "cert-manager", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("re-reading the ApplicationSet: %v", err)
	}
	finalizers, _ := templateFinalizers(obj.Object)
	if containsString(finalizers, ResourcesFinalizer) {
		t.Errorf("the template still stamps %q on generated Applications", ResourcesFinalizer)
	}
}

// TestPreserve_IsIdempotent — the handoff can be re-run (a retried
// migration, a merge callback firing twice), so a second Preserve must be
// a clean no-op rather than an error.
func TestPreserve_IsIdempotent(t *testing.T) {
	r := newTestClient(v3ScaffoldAppSetObject("cert-manager"))
	for i := 0; i < 2; i++ {
		if err := r.Preserve(context.Background(), "cert-manager"); err != nil {
			t.Fatalf("Preserve call %d: %v", i+1, err)
		}
	}
}

// TestReleaseGeneratedApplications_DisarmsOnlyThisAppSetsApplications is
// the braces half: the LIVE Applications lose the marker, and only the
// ones this ApplicationSet owns. Getting the ownership filter wrong would
// disarm somebody else's Applications, which is a quiet way to make an
// unrelated ArgoCD deletion stop cleaning up after itself.
func TestReleaseGeneratedApplications_DisarmsOnlyThisAppSetsApplications(t *testing.T) {
	r := newTestClient(
		v3ScaffoldAppSetObject("cert-manager"),
		generatedApp("cert-manager-prod-eu", "cert-manager", true),
		generatedApp("cert-manager-staging-us", "cert-manager", true),
		generatedApp("someone-elses-app", "not-ours", true),
	)

	released, err := r.ReleaseGeneratedApplications(context.Background(), "cert-manager")
	if err != nil {
		t.Fatalf("ReleaseGeneratedApplications: %v", err)
	}
	if len(released) != 2 {
		t.Fatalf("released = %v, want the two cert-manager Applications", released)
	}

	for _, tc := range []struct {
		name string
		want bool // want the finalizer still present?
	}{
		{"cert-manager-prod-eu", false},
		{"cert-manager-staging-us", false},
		{"someone-elses-app", true},
	} {
		obj, err := r.client.Resource(ApplicationsGroupVersionResource).Namespace("argocd").
			Get(context.Background(), tc.name, metav1.GetOptions{})
		if err != nil {
			t.Fatalf("reading %s: %v", tc.name, err)
		}
		finalizers, _ := objectFinalizers(obj.Object)
		if got := containsString(finalizers, ResourcesFinalizer); got != tc.want {
			t.Errorf("%s: finalizer present = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// TestReleaseGeneratedApplications_IsIdempotent — an Application that
// never had the marker (or already lost it) is left alone and not
// reported, so a re-run does not claim work it did not do.
func TestReleaseGeneratedApplications_IsIdempotent(t *testing.T) {
	r := newTestClient(
		v3ScaffoldAppSetObject("cert-manager"),
		generatedApp("cert-manager-prod-eu", "cert-manager", false),
	)
	released, err := r.ReleaseGeneratedApplications(context.Background(), "cert-manager")
	if err != nil {
		t.Fatalf("ReleaseGeneratedApplications: %v", err)
	}
	if len(released) != 0 {
		t.Errorf("released = %v, want none — nothing was armed", released)
	}
}

func TestDelete_RemovesTheApplicationSet(t *testing.T) {
	r := newTestClient(v3ScaffoldAppSetObject("cert-manager"))
	if err := r.Delete(context.Background(), "cert-manager"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	list, err := r.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 0 {
		t.Errorf("ApplicationSet still there after Delete: %+v", list)
	}
}
