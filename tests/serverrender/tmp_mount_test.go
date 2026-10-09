// Package serverrender holds render-level guards on the Sharko server
// chart (charts/sharko) — the chart an operator actually installs.
package serverrender

import (
	"testing"
)

// TestSharkoContainerHasWritableTmpWithReadOnlyRoot pins that the Sharko
// container mounts a writable /tmp emptyDir when the security context sets
// readOnlyRootFilesystem: true (the chart default). This fixes the TUF cache
// mkdir failure that made every catalogue entry log "verification errored"
// with zero verified (v1.23 through v4.0.2).
func TestSharkoContainerHasWritableTmpWithReadOnlyRoot(t *testing.T) {
	objects := renderServerChart(t)

	// Find the Deployment running the Sharko container.
	var deployment *k8sObject
	for i := range objects {
		if objects[i].Kind == "Deployment" {
			for _, spec := range podSpecs([]k8sObject{objects[i]}) {
				if spec.runsSharko() {
					deployment = &objects[i]
					break
				}
			}
		}
	}
	if deployment == nil {
		t.Fatal("no Deployment running the Sharko image found in the render")
	}

	// The chart default is readOnlyRootFilesystem: true; if that's NOT set
	// then the whole premise is wrong and the test should fail rather than
	// pass for the wrong reason.
	podSpec := deployment.Spec.Template.Spec
	var sharkoContainer *container
	for i := range podSpec.Containers {
		c := &podSpec.Containers[i]
		if c.Name == "sharko" {
			sharkoContainer = c
			break
		}
	}
	if sharkoContainer == nil {
		t.Fatal("Sharko Deployment has no container named 'sharko'")
	}

	// (a) Assert readOnlyRootFilesystem is true on the Sharko container.
	if sharkoContainer.SecurityContext == nil || !sharkoContainer.SecurityContext.ReadOnlyRootFilesystem {
		t.Errorf("Sharko container does not have readOnlyRootFilesystem: true — the test " +
			"premise requires the read-only root. Without it, /tmp is already writable and " +
			"the mount is unnecessary.")
	}

	// (b) Check that /tmp is mounted, find the volume name it uses, and
	// check that volume exists and is an emptyDir.
	var tmpMount *volumeMount
	for i := range sharkoContainer.VolumeMounts {
		if sharkoContainer.VolumeMounts[i].MountPath == "/tmp" {
			tmpMount = &sharkoContainer.VolumeMounts[i]
			break
		}
	}
	if tmpMount == nil {
		t.Errorf("Sharko container has no /tmp volumeMount. With readOnlyRootFilesystem: true, " +
			"the TUF cache default /tmp/sigstore-tuf cannot be created, so catalogue signature " +
			"verification logs 'mkdir /tmp/sigstore-tuf: read-only file system' and reports every " +
			"entry as unverified. Mount a writable /tmp emptyDir to fix.")
	} else {
		// The mount exists; check the volume it references.
		volumeName := tmpMount.Name
		var tmpVolume *volume
		for i := range podSpec.Volumes {
			if podSpec.Volumes[i].Name == volumeName {
				tmpVolume = &podSpec.Volumes[i]
				break
			}
		}
		if tmpVolume == nil {
			t.Errorf("Sharko container mounts /tmp from volume %q, but pod spec has no volume "+
				"with that name", volumeName)
		} else if tmpVolume.EmptyDir == nil {
			t.Errorf("Volume %q (mounted at /tmp) is not an emptyDir — it should be a small "+
				"emptyDir with a sizeLimit to hold the TUF cache", volumeName)
		}

		// (c) Assert the /tmp mount is not readOnly.
		if tmpMount.ReadOnly {
			t.Errorf("Sharko container mounts /tmp with readOnly: true — the TUF cache cannot " +
				"be written. Set readOnly: false (or omit it, since false is the default).")
		}
	}
}
