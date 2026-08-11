package connectioncompare

import (
	"testing"

	"github.com/MoranWeissman/sharko/internal/argosecrets"
	"github.com/MoranWeissman/sharko/internal/models"
)

func TestClassify_SevenModes(t *testing.T) {
	tests := []struct {
		name            string
		in              ClassifyInput
		wantMode        Mode
		wantScope       Scope
		wantRepair      RepairScope
		wantLimitReason bool
	}{
		{
			name: "backend-stored kubeconfig, backend wired, Sharko owns -> full, full repair",
			in: ClassifyInput{
				CredsSource:       models.CredsSourceSecretKubeconfig,
				BackendConfigured: true,
				LiveSecretFound:   true,
				LiveManagedBy:     argosecrets.ManagedByValue,
			},
			wantMode:   ModeBackendStoredCredentials,
			wantScope:  ScopeFull,
			wantRepair: RepairScopeFullConnection,
		},
		{
			name: "EKS token -> limited (fresh token every fetch) but full repair",
			in: ClassifyInput{
				CredsSource:       models.CredsSourceEKSToken,
				BackendConfigured: true,
				LiveSecretFound:   true,
				LiveManagedBy:     argosecrets.ManagedByValue,
			},
			wantMode:        ModeEKSExec,
			wantScope:       ScopeLimited,
			wantRepair:      RepairScopeFullConnection,
			wantLimitReason: true,
		},
		{
			name: "inline kubeconfig -> limited, labels-only repair",
			in: ClassifyInput{
				CredsSource:       models.CredsSourceInlineKubeconfig,
				BackendConfigured: true,
				LiveSecretFound:   true,
				LiveManagedBy:     argosecrets.ManagedByValue,
			},
			wantMode:        ModeInlineKubeconfig,
			wantScope:       ScopeLimited,
			wantRepair:      RepairScopeAddonLabelsOnly,
			wantLimitReason: true,
		},
		{
			name: "self-managed -> addon labels only",
			in: ClassifyInput{
				CredsSource:         models.CredsSourceSecretKubeconfig,
				ConnectionManagedBy: models.ConnectionManagedByUser,
				BackendConfigured:   true,
				LiveSecretFound:     true,
			},
			wantMode:        ModeSelfManaged,
			wantScope:       ScopeAddonLabelsOnly,
			wantRepair:      RepairScopeAddonLabelsOnly,
			wantLimitReason: true,
		},
		{
			name: "adopted -> addon labels only",
			in: ClassifyInput{
				CredsSource:       models.CredsSourceSecretKubeconfig,
				BackendConfigured: true,
				LiveSecretFound:   true,
				LiveManagedBy:     argosecrets.ManagedByValue,
				LiveAdopted:       true,
			},
			wantMode:        ModeAdopted,
			wantScope:       ScopeAddonLabelsOnly,
			wantRepair:      RepairScopeAddonLabelsOnly,
			wantLimitReason: true,
		},
		{
			name: "another tool's ownership marker -> ownership conflict, no repair",
			in: ClassifyInput{
				CredsSource:       models.CredsSourceSecretKubeconfig,
				BackendConfigured: true,
				LiveSecretFound:   true,
				LiveManagedBy:     "some-other-tool",
			},
			wantMode:        ModeForeignOwned,
			wantScope:       ScopeOwnershipConflict,
			wantRepair:      RepairScopeNone,
			wantLimitReason: true,
		},
		{
			name: "empty credsSource -> unknown source, limited, labels-only repair",
			in: ClassifyInput{
				CredsSource:       "",
				BackendConfigured: true,
				LiveSecretFound:   true,
				LiveManagedBy:     argosecrets.ManagedByValue,
			},
			wantMode:        ModeUnknownSource,
			wantScope:       ScopeLimited,
			wantRepair:      RepairScopeAddonLabelsOnly,
			wantLimitReason: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Classify(tt.in)
			if got.Mode != tt.wantMode {
				t.Errorf("Mode = %q, want %q", got.Mode, tt.wantMode)
			}
			if got.Scope != tt.wantScope {
				t.Errorf("Scope = %q, want %q", got.Scope, tt.wantScope)
			}
			if got.RepairScope != tt.wantRepair {
				t.Errorf("RepairScope = %q, want %q", got.RepairScope, tt.wantRepair)
			}
			if tt.wantLimitReason && got.LimitReason == "" {
				t.Error("LimitReason is empty, want a sentence explaining the narrower scope")
			}
			if !tt.wantLimitReason && got.LimitReason != "" {
				t.Errorf("LimitReason = %q, want empty for a full-scope mode", got.LimitReason)
			}
		})
	}
}

// TestClassify_OwnershipConflictBeatsEverything pins the check order. An
// ownership conflict is the answer even for the mode that would otherwise get
// the widest scope — the gate is never weakened to produce a richer
// comparison.
func TestClassify_OwnershipConflictBeatsEverything(t *testing.T) {
	for _, credsSource := range []string{
		models.CredsSourceSecretKubeconfig,
		models.CredsSourceEKSToken,
		models.CredsSourceInlineKubeconfig,
		"",
	} {
		for _, managedBy := range []string{"", models.ConnectionManagedByUser, models.ConnectionManagedBySharko} {
			got := Classify(ClassifyInput{
				CredsSource:         credsSource,
				ConnectionManagedBy: managedBy,
				BackendConfigured:   true,
				LiveSecretFound:     true,
				LiveManagedBy:       "rival-tool",
				LiveAdopted:         true,
			})
			if got.Mode != ModeForeignOwned {
				t.Errorf("credsSource=%q managedBy=%q: Mode = %q, want %q", credsSource, managedBy, got.Mode, ModeForeignOwned)
			}
			if got.RepairScope != RepairScopeNone {
				t.Errorf("credsSource=%q managedBy=%q: RepairScope = %q, want none", credsSource, managedBy, got.RepairScope)
			}
		}
	}
}

// TestClassify_OnlyBackendStoredIsEverFullyCheckable is the guard behind the
// "never a false in-sync" rule: exactly one mode may report a cluster fully in
// sync, and it is the one where Sharko can rebuild every field it owns from
// somewhere other than the live Secret.
func TestClassify_OnlyBackendStoredIsEverFullyCheckable(t *testing.T) {
	fullyCheckable := map[Mode]bool{}
	for _, credsSource := range []string{
		models.CredsSourceSecretKubeconfig,
		models.CredsSourceEKSToken,
		models.CredsSourceInlineKubeconfig,
		"",
		"something-a-future-version-writes",
	} {
		for _, managedBy := range []string{"", models.ConnectionManagedByUser, models.ConnectionManagedBySharko, "User"} {
			for _, backend := range []bool{false, true} {
				for _, found := range []bool{false, true} {
					for _, adopted := range []bool{false, true} {
						for _, live := range []string{"", argosecrets.ManagedByValue, "rival"} {
							p := Classify(ClassifyInput{
								CredsSource:         credsSource,
								ConnectionManagedBy: managedBy,
								BackendConfigured:   backend,
								LiveSecretFound:     found,
								LiveManagedBy:       live,
								LiveAdopted:         adopted,
							})
							if p.Mode == "" || p.Scope == "" || p.RepairScope == "" {
								t.Fatalf("Classify returned an incomplete policy %+v for credsSource=%q managedBy=%q backend=%v found=%v adopted=%v live=%q",
									p, credsSource, managedBy, backend, found, adopted, live)
							}
							if p.FullyCheckable() {
								fullyCheckable[p.Mode] = true
							}
						}
					}
				}
			}
		}
	}
	if len(fullyCheckable) != 1 || !fullyCheckable[ModeBackendStoredCredentials] {
		t.Errorf("modes that can report fully in sync = %v, want only %q", fullyCheckable, ModeBackendStoredCredentials)
	}
}

// TestClassify_UnknownSourceNeverFullNeverFullRepair pins the seventh mode's
// two absolute rules across every other input combination.
func TestClassify_UnknownSourceNeverFullNeverFullRepair(t *testing.T) {
	for _, backend := range []bool{false, true} {
		for _, found := range []bool{false, true} {
			p := Classify(ClassifyInput{
				CredsSource:       "",
				BackendConfigured: backend,
				LiveSecretFound:   found,
				LiveManagedBy:     argosecrets.ManagedByValue,
			})
			if p.Mode != ModeUnknownSource {
				t.Fatalf("backend=%v found=%v: Mode = %q, want %q", backend, found, p.Mode, ModeUnknownSource)
			}
			if p.Scope == ScopeFull {
				t.Errorf("backend=%v found=%v: unknown source got full scope", backend, found)
			}
			if p.RepairScope == RepairScopeFullConnection {
				t.Errorf("backend=%v found=%v: unknown source got a full-connection repair offer", backend, found)
			}
		}
	}
}

// TestClassify_CaseFoldedUserValueStaysSelfManaged mirrors
// models.IsUserManagedConnection's deliberate case-folding for hand-edited
// files: a stray capital must still land on the narrow, guest stance.
func TestClassify_CaseFoldedUserValueStaysSelfManaged(t *testing.T) {
	for _, v := range []string{"user", "User", "USER"} {
		p := Classify(ClassifyInput{
			CredsSource:         models.CredsSourceSecretKubeconfig,
			ConnectionManagedBy: v,
			BackendConfigured:   true,
			LiveSecretFound:     true,
			LiveManagedBy:       argosecrets.ManagedByValue,
		})
		if p.Mode != ModeSelfManaged {
			t.Errorf("connectionManagedBy=%q: Mode = %q, want %q", v, p.Mode, ModeSelfManaged)
		}
	}
}
