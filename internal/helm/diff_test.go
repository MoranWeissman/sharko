package helm

import "testing"

func TestDiffValues(t *testing.T) {
	old := `
image:
  tag: "1.0.0"
replicas: 1
resources:
  limits:
    memory: 512Mi
`
	new := `
image:
  tag: "2.0.0"
replicas: 1
resources:
  limits:
    memory: 1Gi
    cpu: 500m
newField: true
`
	added, removed, changed, err := DiffValues(old, new)
	if err != nil {
		t.Fatal(err)
	}

	if len(changed) != 2 { // tag and memory changed
		t.Errorf("expected 2 changed, got %d", len(changed))
	}
	if len(added) != 2 { // cpu and newField added
		t.Errorf("expected 2 added, got %d", len(added))
	}
	if len(removed) != 0 {
		t.Errorf("expected 0 removed, got %d", len(removed))
	}
}

func TestFindConflicts(t *testing.T) {
	config := `
resources:
  limits:
    memory: 2Gi
`
	oldDefault := `
resources:
  limits:
    memory: 512Mi
`
	newDefault := `
resources:
  limits:
    memory: 1Gi
`
	conflicts, err := FindConflicts(config, oldDefault, newDefault)
	if err != nil {
		t.Fatal(err)
	}

	if len(conflicts) != 1 {
		t.Fatalf("expected 1 conflict, got %d", len(conflicts))
	}
	if conflicts[0].Path != "resources.limits.memory" {
		t.Errorf("expected path resources.limits.memory, got %s", conflicts[0].Path)
	}
}
