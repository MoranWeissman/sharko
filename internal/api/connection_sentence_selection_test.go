package api

// connection_sentence_selection_test.go — GUARD C: for a given server state,
// the builder picks the RIGHT SENTENCE.
//
// # Why this is not another wall of pasted strings
//
// Twenty-two of the sentences on this surface had no exact-string pin at all.
// The obvious fix — paste twenty-two literals into a test — was ruled out by
// the product owner, in these words: "do not create another set of
// hand-copied strings merely to test the first set." They are right. A second
// copy of the words is a second thing to drift, and it tests the one thing
// that was never in doubt (that a constant holds what it holds) while leaving
// the thing that was in doubt untested.
//
// What was never tested is SELECTION. The catalog owns the words; the builder
// owns choosing among them, and choosing wrongly is how a page ends up saying
// "Addon labels out of sync" above a sentence explaining that the Secret does
// not exist. So every assertion here names a catalog IDENTIFIER and not a
// single word of product text.
//
// # The one place text appears, and why it is not a copy
//
// sentenceIDOf turns a response string back into its identifier by looking it
// up in the catalog. It holds no text of its own — it is the inverse of the
// map, and it fails loudly if a response carries a sentence the catalog does
// not know, which is its own useful signal.
//
// Exact literal pins stay where the product owner actually ruled on the
// wording: TestConnectionReconciliation_NewSentencesExact and
// TestConnectionRepair_RefusalSentencesExact. This file does not duplicate
// them.

import (
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/MoranWeissman/sharko/internal/argosecrets"
	"github.com/MoranWeissman/sharko/internal/clusterreconciler"
	"github.com/MoranWeissman/sharko/internal/connectioncompare"
	"github.com/MoranWeissman/sharko/internal/models"
)

// mustBeEmpty is the expectation "this field must carry no sentence at all".
// It is a real claim in several rows — the suppressed EKS qualifier, the
// withheld automatic promise — and writing it as an empty string would be
// indistinguishable from "this row does not care".
const mustBeEmpty = "<empty>"

// sentenceIDOf returns the catalog identifier for a sentence the server
// produced, recording it in `seen` so the coverage roll-up can name the
// identifiers no scenario ever reached.
//
// A response carrying a sentence the catalog does not know is a failure in its
// own right: it means the server ships wording from outside the contract, and
// the browser would have no identifier to render it by.
func sentenceIDOf(t *testing.T, seen map[string]bool, text string) string {
	t.Helper()
	if text == "" {
		return mustBeEmpty
	}
	var found []string
	for id, sentence := range ConnectionSentences {
		if sentence == text {
			found = append(found, id)
		}
	}
	switch len(found) {
	case 0:
		t.Errorf("the server produced a sentence that is not in ConnectionSentences:\n  %q\n"+
			"Every user-facing sentence must be catalogued, or the browser has no identifier to render it by.", text)
		return "<uncatalogued>"
	case 1:
		if seen != nil {
			seen[found[0]] = true
		}
		return found[0]
	default:
		sort.Strings(found)
		t.Errorf("the sentence %q is catalogued under %d identifiers (%s) — selection cannot be asserted unambiguously",
			text, len(found), strings.Join(found, ", "))
		return "<ambiguous>"
	}
}

// wantIDs is the expected identifier for each field a scenario cares about.
// A blank field is not asserted; mustBeEmpty asserts the field carries nothing.
type wantIDs struct {
	headline      string
	qualifier     string
	reason        string
	modeStatement string
	planAutomatic string
	planApproval  string
	// conditions maps a condition ID to the identifier of its detail
	// sentence. Only the conditions a scenario is about need listing.
	conditions map[string]string
}

// --- the state matrix, asserted by identifier --------------------------------

// sentenceSelectionCase is one server state and the identifiers the builder
// must select for it.
type sentenceSelectionCase struct {
	name        string
	view        connectionComparisonView
	healthState string
	selfHealOn  bool
	want        wantIDs
}

// sentenceSelectionCases is the state matrix. It is a package-level function
// rather than a var so each caller gets fresh fixtures, and so the coverage
// roll-up below can drive exactly the same table without depending on which
// test ran first — a global "what did we see" counter would report different
// gaps under `go test -run`.
func sentenceSelectionCases() []sentenceSelectionCase {
	return []sentenceSelectionCase{
		{
			name:        "clean full-scope Sharko-managed connection",
			view:        reconView(nil),
			healthState: healthStateConnected,
			want: wantIDs{
				headline:      "headlineConnectionSynced",
				modeStatement: "modeStatementSharkoManaged",
				conditions: map[string]string{
					conditionGitDefinition:       "condGitDefinitionOK",
					conditionCredentialReference: "condCredentialRefOK",
					conditionOwnership:           "condOwnershipOK",
					conditionLiveSecret:          "condLiveSecretFound",
					conditionComparison:          "condComparisonFull",
					conditionArgoCDConnection:    "condArgoCDConnected",
				},
			},
		},
		{
			name: "EKS connection: everything checkable matched, credential content not comparable",
			view: reconView(func(v *connectionComparisonView) {
				v.Status = string(connectioncompare.StatusLimited)
				v.Scope = string(connectioncompare.ScopeLimited)
				v.CredentialSourceType = models.CredsSourceEKSToken
				reconClassify(v, connectioncompare.ClassifyInput{
					CredsSource:                  models.CredsSourceEKSToken,
					BackendCanProvideStoredFacts: true,
					LiveSecretFound:              true,
				})
				v.LimitReason = v.policy.LimitReason
			}),
			healthState: healthStateConnected,
			want: wantIDs{
				headline: "headlineConfigurationMatchesEKS",
				// Deliberately empty: the headline already says it, and the
				// qualifier is suppressed rather than saying it twice.
				qualifier:     "",
				reason:        "limitReasonEKSNoStoredCredential",
				modeStatement: "modeStatementSharkoManaged",
				conditions: map[string]string{
					conditionCredentialReference: "condCredentialRefEKS",
					conditionComparison:          "condComparisonPartial",
				},
			},
		},
		{
			name: "self-managed connection whose addon labels all match",
			view: reconView(func(v *connectionComparisonView) {
				v.Status = string(connectioncompare.StatusLimited)
				v.Scope = string(connectioncompare.ScopeAddonLabelsOnly)
				reconClassify(v, connectioncompare.ClassifyInput{
					CredsSource:         models.CredsSourceSecretKubeconfig,
					ConnectionManagedBy: models.ConnectionManagedByUser,
					LiveSecretFound:     true,
				})
				v.LimitReason = v.policy.LimitReason
			}),
			healthState: healthStateConnected,
			want: wantIDs{
				headline:      "headlineAddonLabelsSynced",
				qualifier:     "qualifierSelfManaged",
				modeStatement: "modeStatementSelfManaged",
				conditions: map[string]string{
					conditionOwnership:  "condOwnershipGuest",
					conditionLiveSecret: "condLiveSecretFound",
				},
			},
		},
		{
			name: "self-managed connection whose Secret does not exist",
			view: reconView(func(v *connectionComparisonView) {
				v.Status = string(connectioncompare.StatusMissing)
				v.Scope = string(connectioncompare.ScopeAddonLabelsOnly)
				reconClassify(v, connectioncompare.ClassifyInput{
					CredsSource:         models.CredsSourceSecretKubeconfig,
					ConnectionManagedBy: models.ConnectionManagedByUser,
					LiveSecretFound:     false,
				})
			}),
			// ArgoCD said "not checked"; the correction turns it into unknown,
			// which is what selects the no-connection health sentence.
			healthState: healthStateNotChecked,
			want: wantIDs{
				headline:      "headlineConnectionSecretMissing",
				qualifier:     "qualifierSelfManaged",
				reason:        "limitReasonSecretMissingSelfManaged",
				modeStatement: "modeStatementSelfManaged",
				conditions: map[string]string{
					conditionLiveSecret:       "condLiveSecretMissing",
					conditionArgoCDConnection: "condArgoCDNoConnection",
				},
			},
		},
		{
			name: "legacy pasted-credential connection whose Secret is gone",
			view: reconView(func(v *connectionComparisonView) {
				v.Status = string(connectioncompare.StatusMissing)
				v.Scope = string(connectioncompare.ScopeLimited)
				v.CredentialSourceType = models.CredsSourceInlineKubeconfig
				reconClassify(v, connectioncompare.ClassifyInput{
					CredsSource:     models.CredsSourceInlineKubeconfig,
					LiveSecretFound: false,
				})
			}),
			healthState: healthStateConnected,
			want: wantIDs{
				headline:      "headlineOutOfSyncCannotRestore",
				qualifier:     "qualifierLegacyInline",
				reason:        "limitReasonSecretMissingLegacyInline",
				modeStatement: "modeStatementLegacyInline",
			},
		},
		{
			name: "Sharko-managed connection with no Secret, backend readable — creation really is automatic",
			view: reconView(func(v *connectionComparisonView) {
				v.Status = string(connectioncompare.StatusMissing)
				reconClassify(v, connectioncompare.ClassifyInput{
					CredsSource:                  models.CredsSourceSecretKubeconfig,
					BackendCanProvideStoredFacts: true,
					LiveSecretFound:              false,
				})
			}),
			healthState: healthStateConnected,
			want: wantIDs{
				headline:      "headlineOutOfSync",
				reason:        "limitReasonSecretMissingDurable",
				planAutomatic: "planAutomaticSecretCreate",
			},
		},
		{
			name: "Sharko-managed connection with no Secret and an unreadable backend — no promise",
			view: reconView(func(v *connectionComparisonView) {
				v.Status = string(connectioncompare.StatusMissing)
				reconClassify(v, connectioncompare.ClassifyInput{
					CredsSource:                  models.CredsSourceSecretKubeconfig,
					BackendCanProvideStoredFacts: false,
					LiveSecretFound:              false,
				})
			}),
			healthState: healthStateConnected,
			want: wantIDs{
				headline: "headlineOutOfSync",
				reason:   "limitReasonSecretMissingBackendUnreadable",
				// Nothing is promised, because nothing would happen.
				planAutomatic: "",
			},
		},
		{
			name: "connection with no Secret and no credentials source Sharko understands",
			view: reconView(func(v *connectionComparisonView) {
				v.Status = string(connectioncompare.StatusMissing)
				v.CredentialSourceType = ""
				reconClassify(v, connectioncompare.ClassifyInput{
					CredsSource:     "",
					LiveSecretFound: false,
				})
			}),
			healthState: healthStateConnected,
			want: wantIDs{
				reason:        "limitReasonSecretMissingUnknownSource",
				planAutomatic: "",
			},
		},
		{
			name: "connection another tool owns",
			view: reconView(func(v *connectionComparisonView) {
				v.Status = string(connectioncompare.StatusOwnershipConflict)
				v.Scope = string(connectioncompare.ScopeOwnershipConflict)
				reconClassify(v, connectioncompare.ClassifyInput{
					CredsSource:     models.CredsSourceSecretKubeconfig,
					LiveSecretFound: true,
					LiveManagedBy:   "some-other-tool",
				})
				v.LimitReason = v.policy.LimitReason
			}),
			healthState: healthStateConnected,
			want: wantIDs{
				headline: "headlineBlocked",
				// NO reason sentence. The ownership boundary is stated once, by
				// the mode statement; the decision is offered once, by
				// plan.action = take_over. A reason here would be a third copy
				// of both, which is what the product owner ruled out.
				reason:        "",
				modeStatement: "modeStatementForeignOwned",
			},
		},
		{
			name: "connection-configuration drift with the repair door open",
			view: reconView(func(v *connectionComparisonView) {
				v.Status = string(connectioncompare.StatusOutOfSync)
				v.Differences = []connectionComparisonDifference{reconSafeDiff("data.server")}
			}),
			healthState: healthStateConnected,
			want: wantIDs{
				headline:     "headlineOutOfSyncApproval",
				reason:       "reasonOutOfSyncApprovalRequired",
				planApproval: "planRequiresApprovalSentence",
				conditions: map[string]string{
					conditionComparison: "condComparisonDrift",
					conditionApproval:   "condApprovalRequired",
				},
			},
		},
		{
			name: "connection-configuration drift with the repair door withheld",
			view: reconView(func(v *connectionComparisonView) {
				v.Status = string(connectioncompare.StatusOutOfSync)
				v.Differences = []connectionComparisonDifference{reconSafeDiff("data.server")}
				v.RepairAvailable = false
				v.RepairScope = string(connectioncompare.RepairScopeNone)
				v.LimitReason = ""
			}),
			healthState: healthStateConnected,
			want: wantIDs{
				headline: "headlineOutOfSyncApproval",
				reason:   "reasonOutOfSyncRepairWithheld",
				// The withheld door explains its absence instead of pointing
				// at itself.
				planApproval: "reasonOutOfSyncRepairWithheld",
				conditions: map[string]string{
					conditionApproval: "condApprovalNoRepair",
				},
			},
		},
		{
			// R2-1. Git lists this cluster as Sharko's; the live Secret does
			// not carry Sharko's ownership marker. Classify's foreign-owner
			// rule needs a marker that is non-empty AND not Sharko's, so an
			// EMPTY one classifies as an ordinary Sharko-managed connection —
			// which is exactly why the page used to state "Sharko owns this
			// connection Secret." as a passed check about it.
			//
			// The difference is the marker itself, so it is a connection
			// configuration difference and an admin has to approve it; the
			// repair door stays shut because the write path refuses on the
			// same marker.
			name: "Sharko-managed connection whose live Secret carries no ownership marker",
			view: reconView(func(v *connectionComparisonView) {
				v.Status = string(connectioncompare.StatusOutOfSync)
				v.Differences = []connectionComparisonDifference{
					reconSafeDiff("metadata.labels[" + argosecrets.LabelManagedBy + "]"),
				}
				v.liveSecretFound = true
				v.liveOwnershipMarker = ""
				reconClassify(v, connectioncompare.ClassifyInput{
					CredsSource:                  models.CredsSourceSecretKubeconfig,
					BackendCanProvideStoredFacts: true,
					LiveSecretFound:              true,
					LiveManagedBy:                "",
				})
			}),
			healthState: healthStateConnected,
			want: wantIDs{
				headline:     "headlineOutOfSyncApproval",
				reason:       "reasonOutOfSyncRepairWithheld",
				planApproval: "reasonOutOfSyncRepairWithheld",
				conditions: map[string]string{
					conditionOwnership:  "condOwnershipNotMarked",
					conditionComparison: "condComparisonDrift",
					conditionApproval:   "condApprovalNoRepair",
				},
			},
		},
		{
			name: "addon-label drift only, on a v3 repo with self-heal off",
			view: reconView(func(v *connectionComparisonView) {
				v.Status = string(connectioncompare.StatusOutOfSync)
				v.Differences = []connectionComparisonDifference{
					reconSafeDiff("metadata.labels[addons.sharko.dev/datadog]"),
				}
			}),
			healthState: healthStateConnected,
			selfHealOn:  false,
			want: wantIDs{
				headline:      "headlineOutOfSync",
				reason:        "reasonOutOfSyncLabelsOnly",
				planAutomatic: "",
			},
		},
		{
			name: "addon-label drift on a v4 repo, where the reconciler really does converge it",
			view: reconView(func(v *connectionComparisonView) {
				v.Status = string(connectioncompare.StatusOutOfSync)
				v.ComparedPath = clusterreconciler.V4ManagedClustersPath
				v.Differences = []connectionComparisonDifference{
					reconSafeDiff("metadata.labels[addons.sharko.dev/datadog]"),
				}
			}),
			healthState: healthStateConnected,
			want: wantIDs{
				reason:        "reasonOutOfSyncLabelsOnly",
				planAutomatic: "planAutomaticLabelSync",
			},
		},
		{
			name: "self-managed connection whose addon labels drifted",
			view: reconView(func(v *connectionComparisonView) {
				v.Status = string(connectioncompare.StatusOutOfSync)
				v.Scope = string(connectioncompare.ScopeAddonLabelsOnly)
				v.Differences = []connectionComparisonDifference{
					reconSafeDiff("metadata.labels[addons.sharko.dev/datadog]"),
				}
				reconClassify(v, connectioncompare.ClassifyInput{
					CredsSource:         models.CredsSourceSecretKubeconfig,
					ConnectionManagedBy: models.ConnectionManagedByUser,
					LiveSecretFound:     true,
				})
			}),
			healthState: healthStateConnected,
			want: wantIDs{
				headline:      "headlineAddonLabelsOutOfSync",
				reason:        "reasonOutOfSyncLabelsOnly",
				planAutomatic: "planAutomaticLabelSync",
			},
		},
		{
			name: "legacy pasted-credential connection with connection drift",
			view: reconView(func(v *connectionComparisonView) {
				v.Status = string(connectioncompare.StatusOutOfSync)
				v.Scope = string(connectioncompare.ScopeLimited)
				v.CredentialSourceType = models.CredsSourceInlineKubeconfig
				v.Differences = []connectionComparisonDifference{reconSafeDiff("data.server")}
				reconClassify(v, connectioncompare.ClassifyInput{
					CredsSource:     models.CredsSourceInlineKubeconfig,
					LiveSecretFound: true,
				})
				v.LimitReason = v.policy.LimitReason
			}),
			healthState: healthStateConnected,
			want: wantIDs{
				headline:      "headlineOutOfSyncApproval",
				qualifier:     "qualifierLegacyInline",
				reason:        "reasonOutOfSyncLegacyInline",
				planApproval:  "reasonOutOfSyncLegacyInline",
				modeStatement: "modeStatementLegacyInline",
				conditions: map[string]string{
					conditionCredentialReference: "modeStatementLegacyInline",
				},
			},
		},
		{
			name: "legacy pasted-credential connection, nothing wrong in what could be checked",
			view: reconView(func(v *connectionComparisonView) {
				v.Status = string(connectioncompare.StatusLimited)
				v.Scope = string(connectioncompare.ScopeLimited)
				v.CredentialSourceType = models.CredsSourceInlineKubeconfig
				reconClassify(v, connectioncompare.ClassifyInput{
					CredsSource:     models.CredsSourceInlineKubeconfig,
					LiveSecretFound: true,
				})
				v.LimitReason = v.policy.LimitReason
			}),
			healthState: healthStateConnected,
			want: wantIDs{
				headline:  "headlineVerificationIncomplete",
				qualifier: "qualifierLegacyInline",
				reason:    "limitReasonInlineKubeconfig",
			},
		},
		{
			name: "adopted connection — Sharko is a guest on it",
			view: reconView(func(v *connectionComparisonView) {
				v.Status = string(connectioncompare.StatusLimited)
				v.Scope = string(connectioncompare.ScopeAddonLabelsOnly)
				reconClassify(v, connectioncompare.ClassifyInput{
					CredsSource:     models.CredsSourceSecretKubeconfig,
					LiveSecretFound: true,
					LiveAdopted:     true,
				})
				v.LimitReason = v.policy.LimitReason
			}),
			healthState: healthStateConnected,
			want: wantIDs{
				headline:      "headlineAddonLabelsSynced",
				modeStatement: "modeStatementSelfManaged",
				reason:        "",
			},
		},
		{
			name: "record predating the credentials-source field",
			view: reconView(func(v *connectionComparisonView) {
				v.Status = string(connectioncompare.StatusLimited)
				v.Scope = string(connectioncompare.ScopeLimited)
				v.CredentialSourceType = ""
				reconClassify(v, connectioncompare.ClassifyInput{
					CredsSource:     "",
					LiveSecretFound: true,
				})
				v.LimitReason = v.policy.LimitReason
			}),
			healthState: healthStateConnected,
			want: wantIDs{
				reason: "limitReasonSourceNotRecorded",
				conditions: map[string]string{
					conditionCredentialReference: "condCredentialRefUnknownSource",
				},
			},
		},
		{
			name: "record naming a credentials source this Sharko has never heard of",
			view: reconView(func(v *connectionComparisonView) {
				v.Status = string(connectioncompare.StatusLimited)
				v.Scope = string(connectioncompare.ScopeLimited)
				v.CredentialSourceType = "something-from-the-future"
				reconClassify(v, connectioncompare.ClassifyInput{
					CredsSource:     "something-from-the-future",
					LiveSecretFound: true,
				})
				v.LimitReason = v.policy.LimitReason
			}),
			healthState: healthStateConnected,
			want: wantIDs{
				reason: "limitReasonSourceNotUnderstood",
			},
		},
		{
			name: "backend-stored connection whose backend cannot be read right now",
			view: reconView(func(v *connectionComparisonView) {
				v.Status = string(connectioncompare.StatusLimited)
				v.Scope = string(connectioncompare.ScopeLimited)
				reconClassify(v, connectioncompare.ClassifyInput{
					CredsSource:                  models.CredsSourceSecretKubeconfig,
					BackendCanProvideStoredFacts: false,
					LiveSecretFound:              true,
				})
				v.LimitReason = v.policy.LimitReason
			}),
			healthState: healthStateConnected,
			want: wantIDs{
				reason: "limitReasonBackendUnreadable",
				conditions: map[string]string{
					conditionCredentialReference: "condCredentialRefUnreadable",
				},
			},
		},
		{
			name: "a check that could not read Git",
			view: reconView(func(v *connectionComparisonView) {
				setCheckFailure(v, connectioncompare.CheckFailureGitRead)
			}),
			healthState: healthStateConnected,
			want: wantIDs{
				headline: "headlineUnknownCheckFailed",
				reason:   "failGitRead",
				conditions: map[string]string{
					conditionGitDefinition: "condGitDefinitionBlocked",
				},
			},
		},
		{
			name: "a check that could not read the live Secret",
			view: reconView(func(v *connectionComparisonView) {
				setCheckFailure(v, connectioncompare.CheckFailureLiveRead)
			}),
			healthState: healthStateConnected,
			want: wantIDs{
				reason: "failLiveRead",
				conditions: map[string]string{
					conditionGitDefinition: "condGitDefinitionOK",
					conditionLiveSecret:    "condLiveSecretUnread",
				},
			},
		},
		{
			name: "a check that could not read the secrets backend",
			view: reconView(func(v *connectionComparisonView) {
				setCheckFailure(v, connectioncompare.CheckFailureBackendRead)
			}),
			healthState: healthStateConnected,
			want: wantIDs{
				reason: "failBackendRead",
				conditions: map[string]string{
					conditionCredentialReference: "condCredentialRefUnread",
				},
			},
		},
		{
			name: "a check that failed for a reason with no step of its own",
			view: reconView(func(v *connectionComparisonView) {
				setCheckFailure(v, connectioncompare.CheckFailureNoReconciler)
			}),
			healthState: healthStateConnected,
			want: wantIDs{
				reason: "failNoReconciler",
				conditions: map[string]string{
					conditionComparison: "condCheckDidNotFinish",
				},
			},
		},
		{
			name: "a comparison that came back narrower than its own policy claimed",
			view: reconView(func(v *connectionComparisonView) {
				// A provider hot-swap between the policy decision and the read:
				// limited status, full-scope policy, and so no limit sentence
				// to explain it. The reason must never be empty.
				v.Status = string(connectioncompare.StatusLimited)
				v.Scope = string(connectioncompare.ScopeLimited)
				v.LimitReason = ""
			}),
			healthState: healthStateConnected,
			want: wantIDs{
				reason: "reasonVerificationIncomplete",
			},
		},
		{
			name: "drift Sharko can only partly verify — the qualifier says which part",
			view: reconView(func(v *connectionComparisonView) {
				v.Status = string(connectioncompare.StatusOutOfSync)
				v.Scope = string(connectioncompare.ScopeLimited)
				v.Differences = []connectionComparisonDifference{reconSensitiveDiff()}
			}),
			healthState: healthStateConnected,
			want: wantIDs{
				qualifier: "qualifierCredentialNotCompared",
			},
		},
		{
			name: "ArgoCD reports the connection as failing",
			view: reconView(nil),
			// The message rides only with an actual ArgoCD failure.
			healthState: healthStateUnavailable,
			want: wantIDs{
				conditions: map[string]string{
					conditionArgoCDConnection: "condArgoCDUnavailable",
				},
			},
		},
		{
			name:        "ArgoCD has not probed this connection yet",
			view:        reconView(nil),
			healthState: healthStateNotChecked,
			want: wantIDs{
				conditions: map[string]string{
					conditionArgoCDConnection: "condArgoCDNotChecked",
				},
			},
		},
	}
}

// runSentenceSelectionCase drives one state through the builder, asserts every
// identifier the case declares, and records every identifier the response
// carried in `seen`.
func runSentenceSelectionCase(t *testing.T, tc sentenceSelectionCase, seen map[string]bool) {
	t.Helper()
	out := buildConnectionReconciliationView(connectionReconciliationFacts{
		view:        tc.view,
		healthState: tc.healthState,
		selfHealOn:  tc.selfHealOn,
	})

	check := func(field, got, want string) {
		t.Helper()
		if want == "" {
			return // this scenario does not make a claim about this field
		}
		gotID := sentenceIDOf(t, seen, got)
		if gotID != want {
			t.Errorf("%s selected the wrong sentence:\n got id %q (%q)\nwant id %q",
				field, gotID, got, want)
		}
	}

	check("sync.headline", out.Sync.Headline, tc.want.headline)
	check("sync.qualifier", out.Sync.Qualifier, tc.want.qualifier)
	check("sync.reason", out.Sync.Reason, tc.want.reason)
	check("mode_statement", out.ModeStatement, tc.want.modeStatement)
	check("plan.automatic", out.Plan.Automatic, tc.want.planAutomatic)
	check("plan.requires_approval", out.Plan.RequiresApproval, tc.want.planApproval)

	byID := map[string]string{}
	for _, c := range out.Conditions {
		byID[c.ID] = c.Detail
	}
	for condID, wantSentence := range tc.want.conditions {
		detail, ok := byID[condID]
		if !ok {
			t.Errorf("no %q condition was produced at all", condID)
			continue
		}
		check("condition "+condID, detail, wantSentence)
	}

	// Whatever else the response carried, every sentence in it must be
	// catalogued. This is what catches a sentence nobody thought to assert on
	// — including one added tomorrow.
	for _, text := range []string{out.Sync.Headline, out.Sync.Qualifier, out.Sync.Reason,
		out.ModeStatement, out.Plan.Automatic, out.Plan.RequiresApproval} {
		sentenceIDOf(t, seen, text)
	}
	for _, c := range out.Conditions {
		sentenceIDOf(t, seen, c.Detail)
	}
}

func TestConnectionSentences_BuilderSelectsTheRightIdentifier(t *testing.T) {
	for _, tc := range sentenceSelectionCases() {
		t.Run(tc.name, func(t *testing.T) {
			runSentenceSelectionCase(t, tc, nil)
		})
	}
}

// --- the states that are not comparison rows ---------------------------------

// driveNonMatrixSelections covers the canonical and fleet-page states that do
// not come from a finished comparison, recording what they selected in `seen`.
func driveNonMatrixSelections(t *testing.T, seen map[string]bool) {
	t.Helper()

	// A connection no check has reached yet on this server.
	st := connectionCanonicalStateNotChecked(false, models.CredsSourceSecretKubeconfig)
	if got := sentenceIDOf(t, seen, st.Headline); got != "headlineNotCheckedYet" {
		t.Errorf("a connection no check has reached selected %q, want headlineNotCheckedYet", got)
	}
	if st.Qualifier != "" {
		t.Errorf("a never-checked connection needs no qualifier, got %q", st.Qualifier)
	}
	self := connectionCanonicalStateNotChecked(true, models.CredsSourceSecretKubeconfig)
	if got := sentenceIDOf(t, seen, self.Qualifier); got != "qualifierSelfManaged" {
		t.Errorf("a never-checked self-managed connection selected qualifier %q, want qualifierSelfManaged", got)
	}
	legacy := connectionCanonicalStateNotChecked(false, models.CredsSourceInlineKubeconfig)
	if got := sentenceIDOf(t, seen, legacy.Qualifier); got != "qualifierLegacyInline" {
		t.Errorf("a never-checked pasted-credential connection selected qualifier %q, want qualifierLegacyInline", got)
	}

	// The fail-closed invariant guard. Its state is incoherent on purpose:
	// Compare never produces synced at a narrower scope, and the guard exists
	// so a future edit that does cannot ship a false headline.
	guarded := connectionCanonicalStateFor(reconView(func(v *connectionComparisonView) {
		v.Status = string(connectioncompare.StatusSynced)
		v.Scope = string(connectioncompare.ScopeLimited)
	}))
	if guarded.SyncState != syncStateUnknown {
		t.Errorf("the invariant guard did not downgrade the state: got %q", guarded.SyncState)
	}
	if got := sentenceIDOf(t, seen, guarded.Reason); got != "reasonInvariantFailClosed" {
		t.Errorf("the invariant guard selected %q, want reasonInvariantFailClosed", got)
	}

	// The background credential check's own outcomes.
	drifted := reconView(func(v *connectionComparisonView) {
		v.Status = string(connectioncompare.StatusOutOfSync)
		v.Differences = []connectionComparisonDifference{reconSafeDiff("data.server")}
	})
	status, detail := credentialCheckFromView(drifted)
	if status != credentialCheckDrifted {
		t.Errorf("a reported difference gave status %q, want %q", status, credentialCheckDrifted)
	}
	if got := sentenceIDOf(t, seen, detail); got != "credentialDriftNotice" {
		t.Errorf("the drifted detail selected %q, want credentialDriftNotice", got)
	}
	if got := sentenceIDOf(t, seen, credentialDriftClearedNotice); got != "credentialDriftClearedNotice" {
		t.Errorf("the drift-cleared sentence is not catalogued under its own identifier, got %q", got)
	}
	if got := sentenceIDOf(t, seen, credentialCheckRecoveredNotice); got != "credentialCheckRecoveredNotice" {
		t.Errorf("the check-recovered sentence is not catalogued under its own identifier, got %q", got)
	}

	// The limit sentences that only ever reach a person through the FLEET row.
	//
	// A self-managed or adopted connection whose labels all match is reported
	// synced on its own page, with no reason at all — so the connection page
	// never shows these two sentences. The fleet store does: a clean limited
	// comparison is recorded as not_compared, carrying the comparison's own
	// limit sentence as its detail. Driving them here is what stops the
	// catalog from holding two sentences nothing shows is reachable.
	for _, tc := range []struct {
		name   string
		in     connectioncompare.ClassifyInput
		wantID string
	}{
		{"self-managed", connectioncompare.ClassifyInput{
			CredsSource:         models.CredsSourceSecretKubeconfig,
			ConnectionManagedBy: models.ConnectionManagedByUser,
			LiveSecretFound:     true,
		}, "limitReasonSelfManaged"},
		{"adopted", connectioncompare.ClassifyInput{
			CredsSource:     models.CredsSourceSecretKubeconfig,
			LiveSecretFound: true,
			LiveAdopted:     true,
		}, "limitReasonAdopted"},
		{"pasted kubeconfig", connectioncompare.ClassifyInput{
			CredsSource:     models.CredsSourceInlineKubeconfig,
			LiveSecretFound: true,
		}, "limitReasonInlineKubeconfig"},
		{"unreadable backend", connectioncompare.ClassifyInput{
			CredsSource:                  models.CredsSourceSecretKubeconfig,
			BackendCanProvideStoredFacts: false,
			LiveSecretFound:              true,
		}, "limitReasonBackendUnreadable"},
	} {
		policy := connectioncompare.Classify(tc.in)
		limited := reconView(func(v *connectionComparisonView) {
			v.Status = string(connectioncompare.StatusLimited)
			v.Scope = string(policy.Scope)
			v.OwnershipMode = string(policy.Mode)
			v.policy = policy
			v.LimitReason = policy.LimitReason
		})
		gotStatus, gotDetail := credentialCheckFromView(limited)
		if gotStatus != credentialCheckNotCompared {
			t.Errorf("%s: a clean limited comparison gave status %q, want %q",
				tc.name, gotStatus, credentialCheckNotCompared)
		}
		if got := sentenceIDOf(t, seen, gotDetail); got != tc.wantID {
			t.Errorf("%s: the fleet row's detail selected %q, want %q", tc.name, got, tc.wantID)
		}
	}

	// "Background checks are not running, and here is why."
	var noLoop *connectionCheckStatus
	if got := sentenceIDOf(t, seen, noLoop.snapshot().Reason); got != "checkLoopNotScheduled" {
		t.Errorf("a server with no loop selected %q, want checkLoopNotScheduled", got)
	}
	fresh := newConnectionCheckStatus()
	fresh.markScheduled(time.Minute)
	if got := sentenceIDOf(t, seen, fresh.snapshot().Reason); got != "checkLoopNotRunYet" {
		t.Errorf("a scheduled loop with no finished pass selected %q, want checkLoopNotRunYet", got)
	}
	for _, tc := range []struct{ reason, wantID string }{
		{checkLoopNoReconciler, "checkLoopNoReconciler"},
		{checkLoopNoArgoCD, "checkLoopNoArgoCD"},
		{checkLoopClusterListFailed, "checkLoopClusterListFailed"},
		{failNoGitConnection, "failNoGitConnection"},
		{failNoHubClient, "failNoHubClient"},
	} {
		cs := newConnectionCheckStatus()
		cs.markScheduled(time.Minute)
		cs.recordPass(time.Now(), checkLoopPass{Reason: tc.reason})
		snap := cs.snapshot()
		if snap.Running {
			t.Errorf("a pass that checked nothing reported running=true (%s)", tc.wantID)
		}
		if got := sentenceIDOf(t, seen, snap.Reason); got != tc.wantID {
			t.Errorf("the check status selected %q, want %q", got, tc.wantID)
		}
	}
}

func TestConnectionSentences_NonComparisonStatesSelectTheRightIdentifier(t *testing.T) {
	driveNonMatrixSelections(t, nil)
}

// --- the coverage roll-up ----------------------------------------------------

// TestConnectionSentences_EveryCatalogEntryIsAccountedFor names every
// catalogued sentence no scenario in this file ever proved the server can
// select.
//
// It re-drives the same scenarios rather than reading a counter the other
// tests filled in, so it reports the same gaps whether the whole package runs
// or only this test does. A global would have made `go test -run` lie.
//
// It is a LIST, never a count. Without it the table above could quietly stop
// covering half the catalog while every case still in it went on passing.
func TestConnectionSentences_EveryCatalogEntryIsAccountedFor(t *testing.T) {
	seen := map[string]bool{}
	for _, tc := range sentenceSelectionCases() {
		runSentenceSelectionCase(t, tc, seen)
	}
	driveNonMatrixSelections(t, seen)

	var undeclared []string
	for id := range ConnectionSentences {
		if seen[id] {
			continue
		}
		if _, ok := sentencesNotDrivenHere[id]; !ok {
			undeclared = append(undeclared, "  "+id)
		}
	}
	if len(undeclared) > 0 {
		sort.Strings(undeclared)
		t.Errorf("%d catalogued sentence(s) are never proved selectable by any scenario in this file,\n"+
			"and are not declared in sentencesNotDrivenHere. Nothing shows the server can actually\n"+
			"produce them, or that it produces them in the right state:\n%s",
			len(undeclared), strings.Join(undeclared, "\n"))
	}

	// The declarations must stay true. An identifier that IS driven now has to
	// come off the list, or the list slowly becomes the place coverage goes to
	// die.
	for id := range sentencesNotDrivenHere {
		if seen[id] {
			t.Errorf("sentencesNotDrivenHere lists %q, but a scenario in this file now drives it — remove the entry.", id)
		}
		if _, ok := ConnectionSentences[id]; !ok {
			t.Errorf("sentencesNotDrivenHere lists %q, which is not in the catalog at all — remove the stale entry.", id)
		}
	}
	t.Logf("selection proved for %d of %d catalogued sentences; %d declared out of reach here",
		len(seen), len(ConnectionSentences), len(sentencesNotDrivenHere))
}

// sentencesNotDrivenHere names every catalogued sentence this file does not
// prove selectable, and why. Each entry is a gap somebody chose, which is the
// whole point of writing them down.
var sentencesNotDrivenHere = map[string]string{
	"failNotManaged": "a whole-check refusal returned as a 404 before any view is built — driven by the handler tests in connection_comparison_test.go, not by the builder",

	// ── R2-4: the two failures raised INSIDE connectioncompare.Compare ────
	// Both reach a person through sync.reason, exactly like the eight
	// refusals above them, but the scenarios in this file build the view
	// directly and never run Compare, so neither is selected here. Their
	// words are pinned character for character, and their mapping from the
	// typed reason proved in both directions, by
	// TestConnectionFailure_RenderedSentencesAreExact and
	// TestConnectionFailure_EveryTypedReasonHasASentence.
	"failAddonLabelsUnknown": "raised inside connectioncompare.Compare when the cluster's addon file could not be read — a comparison-core state; pinned in connection_failure_typed_test.go",
	"failExpectedBuild":      "raised inside connectioncompare.Compare when building the expected connection fails — a comparison-core state; pinned in connection_failure_typed_test.go",
	"failCheckDidNotFinish":  "the last resort for a typed reason with no sentence of its own — unreachable while the exhaustiveness guard passes, and proved to render something true by TestConnectionFailure_UndeclaredReasonStillSaysSomethingTrue",

	// ── R2-4: why a FIELD was not checked ─────────────────────────────────
	// These ride in comparison.not_checked[].reason, a list this builder
	// copies through without reading. There is no builder state that selects
	// between them, so there is nothing for a scenario here to assert.
	// TestCompare_EKSNotCheckedReasonIsExact pins the first one's wording.
	"reasonEKSNoStoredCredential": "a not_checked[].reason from connectioncompare.Compare — the builder never chooses it; pinned in internal/connectioncompare/eks_no_credential_test.go",
	"reasonNoIndependentCopy":     "a not_checked[].reason from connectioncompare.Compare — the builder never chooses it",
	// B15's two. Same kind as the pair above and reachable the same way:
	// through comparison.not_checked[].reason, which this builder copies
	// without reading. The first is produced whenever Git declares a label
	// that a takeover also recorded as the previous owner's; the second is a
	// backstop nothing in today's code reaches, which is why it is tested by
	// calling the function that would produce it.
	"reasonLabelPreservedForPreviousOwner": "a not_checked[].reason from connectioncompare.Compare — the builder never chooses it; produced and pinned by TestCompare_PreservedLabelGitAlsoDeclaresIsNotChecked and TestNotCheckedLabelReasonsAreExact in internal/connectioncompare/preserved_label_accounting_test.go",
	"reasonLabelNotCompared":               "a not_checked[].reason from connectioncompare.Compare's backstop for a skip nobody has written yet — deliberately unreachable today; produced and pinned by TestCompare_UnaccountedOwnedLabelIsNotChecked and TestNotCheckedLabelReasonsAreExact in internal/connectioncompare/preserved_label_accounting_test.go",

	// ── R2-4: the repair endpoint's own sentences ─────────────────────────
	// Eighteen sentences written by POST /repair, not by the reconciliation
	// view builder this file drives. They were brought under the catalog in
	// R2-4 because nothing covered them at all; proving WHICH repair state
	// selects which one is the repair handler's job, and
	// TestConnectionRepair_RefusalSentencesExact in
	// connection_sentence_guards_test.go pins the refusal wordings.
	"repairFailNoReconciler":            "written by the repair handler, not the reconciliation view builder",
	"repairFailNoHubClient":             "written by the repair handler, not the reconciliation view builder",
	"repairFailGitRead":                 "written by the repair handler, not the reconciliation view builder",
	"repairFailNotManaged":              "written by the repair handler, not the reconciliation view builder",
	"repairFailBuild":                   "written by the repair handler, not the reconciliation view builder",
	"repairFailWrite":                   "written by the repair handler, not the reconciliation view builder",
	"repairFailSecretGoneSharkoCreates": "written by the repair handler, not the reconciliation view builder",
	"repairFailSecretGoneLabelsOnly":    "written by the repair handler, not the reconciliation view builder",
	"repairFailRaced":                   "written by the repair handler, not the reconciliation view builder",
	"repairFailNotOwned":                "written by the repair handler, not the reconciliation view builder",
	"repairFailRevisionUnknown":         "written by the repair handler, not the reconciliation view builder",
	"repairFailRevisionMissing":         "written by the repair handler, not the reconciliation view builder",
	"repairFailRevisionMoved":           "written by the repair handler, not the reconciliation view builder",
	"repairFailAddonLabelsUnknown":      "written by the repair handler, not the reconciliation view builder",
	"repairDoneConnectionChanged":       "the repair handler's own outcome message, not a builder state",
	"repairDoneConnectionUnchanged":     "the repair handler's own outcome message, not a builder state",
	"repairDoneLabelsChanged":           "the repair handler's own outcome message, not a builder state",
	"repairDoneLabelsUnchanged":         "the repair handler's own outcome message, not a builder state",
	"failCredsUnavailable":              "returned by expectedConnectionSpec when no credentials router is wired — a server-wiring path, not a builder state",

	"limitReasonCommitUnknown": "set by compareClusterConnection when the git provider cannot name a commit — a comparison-core state, driven in connection_reconciliation_test.go",

	// The F8 split. buildConnectionReconciliationView picks between this
	// sentence and condArgoCDNotChecked on the argoUnreachable fact, which
	// the scenarios in THIS file do not vary. It is proved selectable, in
	// the right state, by TestConnectionReconciliation_ArgoNotCheckedSentencesSplit
	// in connection_reconciliation_test.go: that case builds the view with
	// healthStateNotChecked AND argoUnreachable set, and asserts the
	// argocd_connection condition carries exactly this sentence.
	"condArgoCDUnreachable": "chosen on the argoUnreachable fact, which this file's scenarios hold constant — driven in connection_reconciliation_test.go",

	// THE NOTE THAT USED TO BE HERE WAS WRONG, and the way it was wrong is
	// worth keeping. It said condOwnershipForeign was unreachable because
	// sync.reason on the foreign row carried the comparison's long
	// LimitReasonForeignOwned while the mode statement was a different
	// sentence. In fact Compare threw the policy's sentence away and
	// substituted a hand-written literal that WAS byte-identical to the mode
	// statement, so the fallback was reached on every ordinary foreign-owned
	// connection — the note described the opposite of what shipped.
	//
	// It was written from reading the mode classifier without following the
	// value through Compare's ownership exit, and nothing failed, because the
	// only sweep that looked at both fields asserts a condition never EQUALS
	// sync.reason — which drifting the two sentences apart makes pass more
	// easily, not less.
	//
	// The product owner has since ruled the foreign-owned page carries no
	// ownership condition row at all, so condOwnershipForeign is gone, both
	// literals with it, and there is nothing left to declare here.
}
