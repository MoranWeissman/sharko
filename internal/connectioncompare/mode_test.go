package connectioncompare

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
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
				CredsSource:                  models.CredsSourceSecretKubeconfig,
				BackendCanProvideStoredFacts: true,
				LiveSecretFound:              true,
				LiveManagedBy:                argosecrets.ManagedByValue,
			},
			wantMode:   ModeBackendStoredCredentials,
			wantScope:  ScopeFull,
			wantRepair: RepairScopeFullConnection,
		},
		{
			name: "EKS token -> limited (fresh token every fetch) but full repair",
			in: ClassifyInput{
				CredsSource:                  models.CredsSourceEKSToken,
				BackendCanProvideStoredFacts: true,
				LiveSecretFound:              true,
				LiveManagedBy:                argosecrets.ManagedByValue,
			},
			wantMode:        ModeEKSToken,
			wantScope:       ScopeLimited,
			wantRepair:      RepairScopeFullConnection,
			wantLimitReason: true,
		},
		{
			name: "inline kubeconfig -> limited, labels-only repair",
			in: ClassifyInput{
				CredsSource:                  models.CredsSourceInlineKubeconfig,
				BackendCanProvideStoredFacts: true,
				LiveSecretFound:              true,
				LiveManagedBy:                argosecrets.ManagedByValue,
			},
			wantMode:        ModeInlineKubeconfig,
			wantScope:       ScopeLimited,
			wantRepair:      RepairScopeAddonLabelsOnly,
			wantLimitReason: true,
		},
		{
			name: "self-managed -> addon labels only",
			in: ClassifyInput{
				CredsSource:                  models.CredsSourceSecretKubeconfig,
				ConnectionManagedBy:          models.ConnectionManagedByUser,
				BackendCanProvideStoredFacts: true,
				LiveSecretFound:              true,
			},
			wantMode:        ModeSelfManaged,
			wantScope:       ScopeAddonLabelsOnly,
			wantRepair:      RepairScopeAddonLabelsOnly,
			wantLimitReason: true,
		},
		{
			name: "adopted -> addon labels only",
			in: ClassifyInput{
				CredsSource:                  models.CredsSourceSecretKubeconfig,
				BackendCanProvideStoredFacts: true,
				LiveSecretFound:              true,
				LiveManagedBy:                argosecrets.ManagedByValue,
				LiveAdopted:                  true,
			},
			wantMode:        ModeAdopted,
			wantScope:       ScopeAddonLabelsOnly,
			wantRepair:      RepairScopeAddonLabelsOnly,
			wantLimitReason: true,
		},
		{
			name: "another tool's ownership marker -> ownership conflict, no repair",
			in: ClassifyInput{
				CredsSource:                  models.CredsSourceSecretKubeconfig,
				BackendCanProvideStoredFacts: true,
				LiveSecretFound:              true,
				LiveManagedBy:                "some-other-tool",
			},
			wantMode:        ModeForeignOwned,
			wantScope:       ScopeOwnershipConflict,
			wantRepair:      RepairScopeNone,
			wantLimitReason: true,
		},
		{
			name: "empty credsSource -> unknown source, limited, labels-only repair",
			in: ClassifyInput{
				CredsSource:                  "",
				BackendCanProvideStoredFacts: true,
				LiveSecretFound:              true,
				LiveManagedBy:                argosecrets.ManagedByValue,
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
				CredsSource:                  credsSource,
				ConnectionManagedBy:          managedBy,
				BackendCanProvideStoredFacts: true,
				LiveSecretFound:              true,
				LiveManagedBy:                "rival-tool",
				LiveAdopted:                  true,
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
								CredsSource:                  credsSource,
								ConnectionManagedBy:          managedBy,
								BackendCanProvideStoredFacts: backend,
								LiveSecretFound:              found,
								LiveManagedBy:                live,
								LiveAdopted:                  adopted,
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
				CredsSource:                  "",
				BackendCanProvideStoredFacts: backend,
				LiveSecretFound:              found,
				LiveManagedBy:                argosecrets.ManagedByValue,
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

// TestClassify_EKSModeIsCalledTokenNotExec pins the wire string and the Go
// identifier together.
//
// The mode used to be called eks_exec, and that was not true: the writer for
// this source supplies a minted bearer token, so BuildClusterSecret emits the
// bearerToken shape, not an execProviderConfig. The name now says what the code
// actually produces, and the wire value and the Go name agree.
func TestClassify_EKSModeIsCalledTokenNotExec(t *testing.T) {
	if got := string(ModeEKSToken); got != "eks_token" {
		t.Errorf("the EKS mode's wire value = %q, want %q", got, "eks_token")
	}
	p := Classify(ClassifyInput{
		CredsSource:                  models.CredsSourceEKSToken,
		BackendCanProvideStoredFacts: true,
		LiveSecretFound:              true,
		LiveManagedBy:                argosecrets.ManagedByValue,
	})
	if p.Mode != ModeEKSToken {
		t.Fatalf("Mode = %q, want %q", p.Mode, ModeEKSToken)
	}
	if string(p.Mode) == "eks_exec" {
		t.Error("the mode is still called exec, which describes a Secret shape Sharko does not write for this source")
	}
}

// TestClassify_EKSTokenLimitReasonIsExact pins the EKS sentence character for
// character. The product owner signed off this exact wording; a paraphrase is a
// change to the product, not a tidy-up.
//
// The old sentence ended "Everything else about the connection is checked",
// which was not true — the annotation set is deliberately empty and the guest
// rules narrow the label set, so "everything else" was a claim wider than the
// code. The new one names what was actually checked.
func TestClassify_EKSTokenLimitReasonIsExact(t *testing.T) {
	const want = "Sharko checked the Secret identity, type, server, and owned labels. It did not compare `data.config`, because the EKS sign-in token changes every time it is created."

	p := Classify(ClassifyInput{
		CredsSource:                  models.CredsSourceEKSToken,
		BackendCanProvideStoredFacts: true,
		LiveSecretFound:              true,
		LiveManagedBy:                argosecrets.ManagedByValue,
	})
	if p.LimitReason != want {
		t.Errorf("the EKS limit reason was changed.\n got: %q\nwant: %q\n\nThis wording was signed off exactly as it is. If it really needs to change, change it with the product owner and update this test in the same commit.", p.LimitReason, want)
	}
	if LimitReasonEKSTokenChangesEveryTime != want {
		t.Errorf("the constant no longer holds the signed-off sentence: %q", LimitReasonEKSTokenChangesEveryTime)
	}
	if strings.Contains(p.LimitReason, "Everything else about the connection is checked") {
		t.Error("the old, untrue \"everything else\" claim is back")
	}
}

// TestClassify_UnrecognisedSourceIsNeverTrusted is the guard on the allowlist.
//
// Before the allowlist, Classify handled the sources it knew and then FELL
// THROUGH to the widest, most trusting answer — full scope and a full-connection
// repair — for anything else. So a typo, a value from a newer Sharko, or
// anything an attacker got into the git record was handed the most trusting
// treatment available. Every value below must land on unknown_source with a
// narrow scope and no full repair.
func TestClassify_UnrecognisedSourceIsNeverTrusted(t *testing.T) {
	values := []struct {
		name  string
		value string
	}{
		{"a typo of a real one", "secret-kubeconfg"},
		{"another typo of a real one", "eks-tokenn"},
		{"a made-up value", "vault-dynamic-lease"},
		{"something a future version might write", "workload-identity-federation"},
		{"the right value in the wrong case", "Secret-Kubeconfig"},
		{"the right value shouted", "EKS-TOKEN"},
		{"the right value with leading whitespace", " secret-kubeconfig"},
		{"the right value with trailing whitespace", "eks-token "},
		{"the right value with a tab in it", "secret\tkubeconfig"},
		{"the right value with a newline", "eks-token\n"},
		{"an injection attempt in the value", "secret-kubeconfig'; DROP TABLE clusters;--"},
		{"a path traversal attempt in the value", "../../etc/passwd"},
		{"a template injection attempt", "{{ .Values.credsSource }}"},
		{"a very long value", string(make([]byte, 4096))},
	}

	for _, v := range values {
		t.Run(v.name, func(t *testing.T) {
			// Every other input set to the most permissive combination there
			// is, so the only thing under test is the unrecognised source.
			for _, backend := range []bool{false, true} {
				p := Classify(ClassifyInput{
					CredsSource:                  v.value,
					BackendCanProvideStoredFacts: backend,
					LiveSecretFound:              true,
					LiveManagedBy:                argosecrets.ManagedByValue,
				})
				if p.Mode != ModeUnknownSource {
					t.Errorf("backend=%v: Mode = %q, want %q — an unrecognised credentials source must never be given a recognised mode", backend, p.Mode, ModeUnknownSource)
				}
				if p.Scope != ScopeLimited {
					t.Errorf("backend=%v: Scope = %q, want %q", backend, p.Scope, ScopeLimited)
				}
				if p.FullyCheckable() {
					t.Errorf("backend=%v: an unrecognised credentials source was reported as fully checkable, which lets it claim in sync", backend)
				}
				if p.RepairScope == RepairScopeFullConnection {
					t.Errorf("backend=%v: an unrecognised credentials source was offered a full-connection repair", backend)
				}
				if p.LimitReason == "" {
					t.Errorf("backend=%v: a narrower scope must explain itself", backend)
				}
			}
		})
	}
}

// TestClassify_EmptyAndUnrecognisedSayDifferentThings pins that the two
// situations behind unknown_source get their own sentence. They are the same
// scope but not the same problem: one record is old and never had the
// information, the other has information Sharko cannot make sense of. Telling
// someone "your record is old" when the real problem is a typo sends them
// looking in the wrong place.
func TestClassify_EmptyAndUnrecognisedSayDifferentThings(t *testing.T) {
	base := ClassifyInput{
		BackendCanProvideStoredFacts: true,
		LiveSecretFound:              true,
		LiveManagedBy:                argosecrets.ManagedByValue,
	}

	empty := base
	empty.CredsSource = ""
	unrecognised := base
	unrecognised.CredsSource = "something-nobody-planned-for"

	emptyPolicy := Classify(empty)
	unrecognisedPolicy := Classify(unrecognised)

	if emptyPolicy.Mode != ModeUnknownSource || unrecognisedPolicy.Mode != ModeUnknownSource {
		t.Fatalf("both must be %q; got %q and %q", ModeUnknownSource, emptyPolicy.Mode, unrecognisedPolicy.Mode)
	}
	if emptyPolicy.LimitReason == unrecognisedPolicy.LimitReason {
		t.Fatalf("an empty source and an unrecognised source give the same sentence, so a person cannot tell which problem they have: %q", emptyPolicy.LimitReason)
	}
	if emptyPolicy.LimitReason != LimitReasonSourceNotRecorded {
		t.Errorf("empty source reason = %q, want the not-recorded sentence", emptyPolicy.LimitReason)
	}
	if unrecognisedPolicy.LimitReason != LimitReasonSourceNotUnderstood {
		t.Errorf("unrecognised source reason = %q, want the not-understood sentence", unrecognisedPolicy.LimitReason)
	}
}

// TestClassify_LimitReasonsAreWrittenForPeople keeps the user-facing sentences
// free of code. A person reads these; a Go identifier, a struct field name or a
// YAML key in one of them is a leak of the implementation into the product.
func TestClassify_LimitReasonsAreWrittenForPeople(t *testing.T) {
	// Words that would only appear if somebody named an internal thing. The
	// EKS sentence deliberately names data.config, which is a real thing the
	// person can see in the Secret, so it is checked separately by its own
	// exact-string test and skipped here.
	banned := []string{
		"credsSource", "creds_source", "CredsSource",
		"ClassifyInput", "connectionManagedBy", "managed-clusters.yaml",
		"nil", "struct", "func ",
	}
	reasons := []string{
		LimitReasonSourceNotRecorded,
		LimitReasonSourceNotUnderstood,
	}
	for _, r := range reasons {
		for _, b := range banned {
			if regexp.MustCompile(`(?i)` + regexp.QuoteMeta(b)).MatchString(r) {
				t.Errorf("a sentence a person reads names an internal thing (%q): %q", b, r)
			}
		}
	}
}

// TestClassify_EveryModelsCredsSourceConstantIsOnTheAllowlist is the loud trap.
//
// The allowlist in mode.go is the whole safety property of Point 1: a value that
// is not on it gets the narrow, no-full-repair treatment. That is correct for a
// typo. It is WRONG, and silently wrong, for a source Sharko genuinely supports
// — every cluster using it would be reported at a narrower scope than it
// deserves, forever, with nobody noticing.
//
// So this test reads the credential-source constants straight out of
// internal/models/credlookup.go and fails if any of them is missing from the
// allowlist. It reads the source file rather than a hand-written list, because a
// hand-written list is the thing that goes stale.
func TestClassify_EveryModelsCredsSourceConstantIsOnTheAllowlist(t *testing.T) {
	// deliberatelyUnsupported is the escape hatch: a credential source that
	// internal/models knows about and this comparison genuinely cannot handle.
	// Empty today — all three known sources are handled. Adding an entry needs
	// a comment saying why, so the narrow treatment is a decision and not an
	// oversight.
	deliberatelyUnsupported := map[string]string{}

	path := filepath.Join("..", "models", "credlookup.go")
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}

	// Matches lines like:  CredsSourceEKSToken = "eks-token"
	re := regexp.MustCompile(`(?m)^\s*(CredsSource\w+)\s*=\s*"([^"]+)"`)
	matches := re.FindAllStringSubmatch(string(body), -1)
	if len(matches) == 0 {
		t.Fatalf("found no CredsSource constants in %s — this test can no longer see what it is guarding, so fix the pattern rather than deleting the test", path)
	}

	for _, m := range matches {
		constName, value := m[1], m[2]
		if why, exempt := deliberatelyUnsupported[value]; exempt {
			t.Logf("%s = %q is deliberately not on the allowlist: %s", constName, value, why)
			continue
		}
		if !supportedCredsSources[value] {
			t.Errorf(`internal/models declares %s = %q, and it is NOT on connectioncompare's supportedCredsSources allowlist.

WHAT THIS MEANS: every cluster recorded with %q is being classified as unknown_source. It gets a limited scope, it can never report in sync, and it is never offered a full repair — silently, forever.

WHAT TO DO: decide, on purpose, which it is.
  - Sharko really supports this source: add %s to supportedCredsSources in internal/connectioncompare/mode.go AND add a branch for it in Classify, so it gets a mode, a scope and a repair limit that are true for it.
  - Sharko does not support it here: leave the allowlist alone and add %q to this test's deliberatelyUnsupported list below with a comment saying why.

Do not "fix" this by deleting the test.`, constName, value, value, constName, value)
		}
	}
}

// TestClassify_CaseFoldedUserValueStaysSelfManaged mirrors
// models.IsUserManagedConnection's deliberate case-folding for hand-edited
// files: a stray capital must still land on the narrow, guest stance.
func TestClassify_CaseFoldedUserValueStaysSelfManaged(t *testing.T) {
	for _, v := range []string{"user", "User", "USER"} {
		p := Classify(ClassifyInput{
			CredsSource:                  models.CredsSourceSecretKubeconfig,
			ConnectionManagedBy:          v,
			BackendCanProvideStoredFacts: true,
			LiveSecretFound:              true,
			LiveManagedBy:                argosecrets.ManagedByValue,
		})
		if p.Mode != ModeSelfManaged {
			t.Errorf("connectionManagedBy=%q: Mode = %q, want %q", v, p.Mode, ModeSelfManaged)
		}
	}
}
