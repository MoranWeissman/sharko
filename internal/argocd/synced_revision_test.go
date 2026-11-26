package argocd

import (
	"encoding/json"
	"testing"
)

// Pre-release walk 4: the cluster page showed "Running (in cluster) 99.0.0"
// for a chart version that does not exist, because Sharko showed the version
// that was ASKED for (spec targetRevision) as the one that runs. The version
// that runs is the one of the last sync that finished.
func TestToModel_SyncedRevision_IsWhatWasDeployed_NotWhatWasAskedFor(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{
			name: "multi-source: asked for a version that does not exist, 6.9.0 still deployed",
			raw: `{"metadata":{"name":"podinfo-spoke-1"},
				"spec":{"sources":[{"repoURL":"https://git.example/repo","targetRevision":"main","ref":"values"},{"repoURL":"https://charts.example","chart":"podinfo","targetRevision":"99.0.0"}]},
				"status":{"sync":{"status":"Unknown"},
				"history":[{"id":3,"revisions":["aaa","6.8.0"]},{"id":4,"revisions":["bbb","6.9.0"]}],
				"conditions":[{"type":"ComparisonError","message":"chart not found"}]}}`,
			want: "6.9.0",
		},
		{
			name: "single source with history",
			raw: `{"metadata":{"name":"x"},"spec":{"source":{"repoURL":"https://charts.example","chart":"podinfo","targetRevision":"6.8.0"}},
				"status":{"sync":{"status":"OutOfSync","revision":"6.8.0"},"history":[{"id":1,"revision":"6.7.1"}]}}`,
			want: "6.7.1",
		},
		{
			name: "no history but Synced: what it was compared against runs",
			raw: `{"metadata":{"name":"x"},"spec":{"source":{"repoURL":"https://charts.example","chart":"podinfo","targetRevision":"6.8.0"}},
				"status":{"sync":{"status":"Synced","revision":"6.8.0"}}}`,
			want: "6.8.0",
		},
		{
			name: "nothing deployed yet",
			raw: `{"metadata":{"name":"x"},"spec":{"source":{"repoURL":"https://charts.example","chart":"podinfo","targetRevision":"6.8.0"}},
				"status":{"sync":{"status":"OutOfSync"}}}`,
			want: "",
		},
		{
			name: "history out of order: the highest id is the newest",
			raw: `{"metadata":{"name":"x"},"spec":{"source":{"repoURL":"https://charts.example","chart":"podinfo","targetRevision":"6.9.0"}},
				"status":{"sync":{"status":"Synced","revision":"6.9.0"},"history":[{"id":7,"revision":"6.9.0"},{"id":2,"revision":"6.7.1"}]}}`,
			want: "6.9.0",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var item argocdApplicationItem
			if err := json.Unmarshal([]byte(tc.raw), &item); err != nil {
				t.Fatal(err)
			}
			got := item.toModel()
			if got.SyncedRevision != tc.want {
				t.Errorf("SyncedRevision = %q, want %q (asked for %q)", got.SyncedRevision, tc.want, got.SourceTargetRevision)
			}
		})
	}
}
