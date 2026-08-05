package remoteclient

import (
	"context"
	"errors"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

// secrets_test.go — P1-A A1 at the choke point.
//
// EnsureSecret and DeleteSecretIfManaged are the two doors every addon
// secret write and delete goes through. The ownership question is asked
// here, once, so no caller can forget to ask it.

func mutations(client *fake.Clientset) []string {
	var out []string
	for _, a := range client.Actions() {
		switch a.GetVerb() {
		case "create", "update", "patch", "delete", "delete-collection":
			out = append(out, a.GetVerb())
		}
	}
	return out
}

func sharkosSecret() *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "addon-creds",
			Namespace: "monitoring",
			Labels:    map[string]string{managedByLabel: managedByValue},
		},
		Data: map[string][]byte{"api-key": []byte("old")},
	}
}

func somebodyElsesSecret(owner string) *corev1.Secret {
	s := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "addon-creds", Namespace: "monitoring"},
		Data:       map[string][]byte{"api-key": []byte("theirs")},
	}
	if owner != "" {
		s.Labels = map[string]string{managedByLabel: owner}
	}
	return s
}

func TestEnsureSecret_CreatesWhenAbsentAndLabelsItAtBirth(t *testing.T) {
	client := fake.NewSimpleClientset()
	if err := EnsureSecret(context.Background(), client, "monitoring", "addon-creds", map[string][]byte{"api-key": []byte("new")}); err != nil {
		t.Fatalf("EnsureSecret: %v", err)
	}
	live, err := client.CoreV1().Secrets("monitoring").Get(context.Background(), "addon-creds", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("reading the secret back: %v", err)
	}
	if live.Labels[managedByLabel] != managedByValue {
		t.Errorf("labels = %v, want Sharko's ownership label", live.Labels)
	}
}

func TestEnsureSecret_UpdatesItsOwn(t *testing.T) {
	client := fake.NewSimpleClientset(sharkosSecret())
	if err := EnsureSecret(context.Background(), client, "monitoring", "addon-creds", map[string][]byte{"api-key": []byte("new")}); err != nil {
		t.Fatalf("EnsureSecret: %v", err)
	}
	live, _ := client.CoreV1().Secrets("monitoring").Get(context.Background(), "addon-creds", metav1.GetOptions{})
	if string(live.Data["api-key"]) != "new" {
		t.Errorf("value = %q, want the rotated one", live.Data["api-key"])
	}
}

func TestEnsureSecret_RefusesSomebodyElses(t *testing.T) {
	for _, owner := range []string{"external-secrets", "helm", ""} {
		client := fake.NewSimpleClientset(somebodyElsesSecret(owner))
		err := EnsureSecret(context.Background(), client, "monitoring", "addon-creds", map[string][]byte{"api-key": []byte("new")})
		if !errors.Is(err, ErrForeignSecret) {
			t.Fatalf("owner=%q: error = %v, want ErrForeignSecret", owner, err)
		}
		if got := mutations(client); len(got) != 0 {
			t.Errorf("owner=%q: wrote %v", owner, got)
		}
		live, _ := client.CoreV1().Secrets("monitoring").Get(context.Background(), "addon-creds", metav1.GetOptions{})
		if string(live.Data["api-key"]) != "theirs" {
			t.Errorf("owner=%q: content changed to %q", owner, live.Data["api-key"])
		}
		if owner != "" && live.Labels[managedByLabel] != owner {
			t.Errorf("owner=%q: the ownership label was rewritten to %q", owner, live.Labels[managedByLabel])
		}
		if owner == "" && live.Labels[managedByLabel] != "" {
			t.Errorf("an unlabeled secret was stamped as Sharko's: %v", live.Labels)
		}
	}
}

func TestDeleteSecretIfManaged_DeletesItsOwn(t *testing.T) {
	client := fake.NewSimpleClientset(sharkosSecret())
	deleted, err := DeleteSecretIfManaged(context.Background(), client, "monitoring", "addon-creds")
	if err != nil {
		t.Fatalf("DeleteSecretIfManaged: %v", err)
	}
	if !deleted {
		t.Error("deleted = false, want true for Sharko's own secret")
	}
	if _, err := client.CoreV1().Secrets("monitoring").Get(context.Background(), "addon-creds", metav1.GetOptions{}); err == nil {
		t.Error("the secret is still there")
	}
}

func TestDeleteSecretIfManaged_RefusesSomebodyElses(t *testing.T) {
	for _, owner := range []string{"external-secrets", ""} {
		client := fake.NewSimpleClientset(somebodyElsesSecret(owner))
		deleted, err := DeleteSecretIfManaged(context.Background(), client, "monitoring", "addon-creds")
		if !errors.Is(err, ErrForeignSecret) {
			t.Fatalf("owner=%q: error = %v, want ErrForeignSecret", owner, err)
		}
		if deleted {
			t.Errorf("owner=%q: claimed to have deleted somebody else's secret", owner)
		}
		if got := mutations(client); len(got) != 0 {
			t.Errorf("owner=%q: performed %v", owner, got)
		}
		if _, err := client.CoreV1().Secrets("monitoring").Get(context.Background(), "addon-creds", metav1.GetOptions{}); err != nil {
			t.Errorf("owner=%q: somebody else's secret was deleted: %v", owner, err)
		}
	}
}

func TestDeleteSecretIfManaged_AlreadyGoneIsNotAnError(t *testing.T) {
	client := fake.NewSimpleClientset()
	deleted, err := DeleteSecretIfManaged(context.Background(), client, "monitoring", "addon-creds")
	if err != nil {
		t.Fatalf("a secret that is already gone must not be an error: %v", err)
	}
	if deleted {
		t.Error("deleted = true for a secret that was never there")
	}
}
