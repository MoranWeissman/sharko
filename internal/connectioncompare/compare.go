package connectioncompare

// compare.go — CC2-3, the read-only comparison.
//
// Compare is a pure function. It takes what the caller already gathered and
// returns an answer. It has no Kubernetes client, no git client, no secrets
// backend, no clock and no network, so there is no path through it that can
// write anything, and the same inputs always produce the same answer — which
// is also what makes "the same request gives the same result" testable rather
// than hopeful.
//
// The expected side is always built by argosecrets.BuildClusterSecret, the one
// canonical builder. This file does not assemble a Secret of its own and must
// never start: a second builder would drift from the writers, and then the
// comparison would report differences that only exist between two copies of
// Sharko's own idea of the truth.

import (
	"crypto/subtle"

	corev1 "k8s.io/api/core/v1"

	"github.com/MoranWeissman/sharko/internal/argosecrets"
	"github.com/MoranWeissman/sharko/internal/models"
)

// Status is the one-word answer for a connection.
type Status string

const (
	// StatusSynced — everything inside the declared scope was checked
	// successfully and matched. Only ever possible at ScopeFull.
	StatusSynced Status = "synced"

	// StatusOutOfSync — at least one field Sharko owns does not match.
	StatusOutOfSync Status = "out_of_sync"

	// StatusMissing — there is no ArgoCD connection Secret for this cluster
	// at all.
	StatusMissing Status = "missing"

	// StatusCheckFailed — Sharko could not finish the check. This is NEVER
	// turned into out_of_sync or synced, on any path, for any reason: not
	// knowing is a different answer from knowing something is wrong, and a
	// user acting on the wrong one of those can break a working cluster.
	StatusCheckFailed Status = "check_failed"

	// StatusOwnershipConflict — the connection is marked as owned by another
	// tool. Nothing was compared.
	StatusOwnershipConflict Status = "ownership_conflict"

	// StatusLimited — everything Sharko could check matched, but the scope
	// was narrower than the whole connection, so this is not a claim that the
	// connection is fully right.
	StatusLimited Status = "limited"
)

// NotCheckedField is one field inside the nominal scope that Sharko
// deliberately did not check, with a fixed safe sentence saying why. It exists
// so a narrower answer says which part it is narrower about, instead of leaving
// the user to guess.
type NotCheckedField struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

// Reasons a field is not checked. Fixed literals — never a provider error,
// never anything derived from a value.
const (
	reasonFreshTokenEveryFetch = "This cluster's sign-in details are issued fresh every time, so they are never the same twice and there is nothing stable to compare them against."
	reasonNoIndependentCopy    = "Sharko has no copy of these details outside the connection itself, so the only thing it could compare them against is the connection — which would always agree and tell you nothing."
)

// On a connection Sharko does not own, the connection details are not listed
// as unchecked either. They are not narrower-than-hoped parts of Sharko's job
// — they are not Sharko's job at all, and the mode's own LimitReason already
// says so in one sentence. Listing them would read as "Sharko wanted to check
// this and could not", which is the wrong story.

// Request is everything Compare is allowed to look at. Value-only and
// deliberately narrow: there is no field a caller-supplied candidate value, an
// expected manifest, a hash, a backend path or a namespace override could
// arrive through, so the comparison cannot be steered into being a
// value-guessing oracle. Growing this struct is where that guarantee would be
// lost — read the endpoint's doc comment before adding a field.
type Request struct {
	// ClusterName is the cluster, which is also the Secret's name.
	ClusterName string

	// Namespace is the namespace Sharko's own reconciler writes connection
	// Secrets into. Taken from the running reconciler, never from a caller.
	Namespace string

	// Policy is the mode policy from Classify.
	Policy Policy

	// Live is the connection Secret as it exists right now, or nil when there
	// is none. Never used to build the expected side.
	Live *corev1.Secret

	// LiveFound distinguishes "no Secret exists" (false, an answer) from "the
	// read failed" (CheckFailure set).
	LiveFound bool

	// DesiredAddonLabels is the addon-enablement label set git declares for
	// this cluster, from the reconciler's own desiredAddonLabels.
	DesiredAddonLabels map[string]string

	// AddonLabelsKnown is false when a v4 cluster-addons file could not be
	// read or parsed. The reconciler refuses to touch labels in that state,
	// and the comparison refuses to call them wrong — it becomes a
	// check_failed, not a difference.
	AddonLabelsKnown bool

	// ConnectivityCheckOn is the live answer to "is the connectivity-check
	// feature switched on", from the same combination of the static flag and
	// the probe_mode setting the write path uses.
	ConnectivityCheckOn bool

	// ExpectedSpec is the credential spec rebuilt INDEPENDENTLY of the live
	// Secret — from the secrets backend plus git. nil when the mode does not
	// allow an independent rebuild (see
	// models.ExpectedCredentialsRebuildableWithoutLiveSecret), in which case
	// the credential fields are reported as not checked rather than compared.
	//
	// A caller that fills this in from the live Secret has broken the whole
	// feature. Nothing in this package reads the live Secret to produce it,
	// and the endpoint that builds it goes through the secrets backend.
	ExpectedSpec *argosecrets.ClusterSecretSpec

	// CheckFailure, when non-empty, is a fixed safe sentence saying Sharko
	// could not finish. Set it and the answer is check_failed, full stop.
	CheckFailure string
}

// Result is the comparison's answer. Nothing in it carries credential
// material: the only values that can appear are Secret type, label values, the
// cluster name, the API server address, data key names, and the fixed
// sentences in this file.
type Result struct {
	Status      Status
	Scope       Scope
	Mode        Mode
	LimitReason string

	// Differences are the fields that did not match, in a fixed order.
	Differences []Difference

	// NotChecked are fields inside the nominal scope Sharko deliberately did
	// not check, each with a reason.
	NotChecked []NotCheckedField

	// CheckedFieldCount is how many fields were actually compared. It is a
	// count of FIELDS, not of bytes or characters of any value.
	CheckedFieldCount int

	// FailureReason is the fixed safe sentence for a check_failed answer.
	FailureReason string

	// RepairAvailable and RepairScope come straight from the policy. Step 2
	// never repairs; these say what step 3 would be allowed to do.
	RepairAvailable bool
	RepairScope     RepairScope
}

// Compare answers whether one cluster's connection Secret matches what Sharko
// means it to be.
//
// The order of the early exits is the honesty order:
//
//  1. A failure the caller already hit is check_failed. It is never softened
//     into a difference or into agreement.
//  2. An ownership conflict compares nothing.
//  3. No Secret at all is missing, not out of sync — there is no drift
//     between something and nothing.
//  4. Not knowing what the labels should be is check_failed, because calling
//     labels wrong when Sharko could not read what right is would point the
//     user at a repair that would delete their addons.
//  5. Then, and only then, fields are compared.
func Compare(req Request) Result {
	res := Result{
		Scope:       req.Policy.Scope,
		Mode:        req.Policy.Mode,
		LimitReason: req.Policy.LimitReason,
		// R3-8: RepairAvailable and RepairScope computed AFTER status is known,
		// not before. check_failed, missing and ownership_conflict must all
		// return repair_available: false.
	}

	// 1. Sharko could not finish.
	if req.CheckFailure != "" {
		res.Status = StatusCheckFailed
		res.FailureReason = req.CheckFailure
		res.RepairAvailable = false
		res.RepairScope = RepairScopeNone
		return res
	}

	// 2. Somebody else owns it.
	if req.Policy.Scope == ScopeOwnershipConflict {
		res.Status = StatusOwnershipConflict
		res.LimitReason = "Another tool owns this connection, so Sharko will not change it."
		res.RepairAvailable = false
		res.RepairScope = RepairScopeNone
		return res
	}

	// 3. There is nothing there.
	if !req.LiveFound || req.Live == nil {
		res.Status = StatusMissing
		res.LimitReason = "This cluster has no connection Secret yet. The reconciler will create it on its next pass."
		res.RepairAvailable = false
		res.RepairScope = RepairScopeNone
		return res
	}

	// 4. Sharko does not know what the labels should be.
	if !req.AddonLabelsKnown {
		res.Status = StatusCheckFailed
		res.FailureReason = "Sharko could not read which addons should be on for this cluster, so it cannot tell whether this connection's labels are right. Check that the cluster's addon file is readable in git, then check again."
		res.RepairAvailable = false
		res.RepairScope = RepairScopeNone
		return res
	}

	expectedLabels, expectedSecret, buildFailed := expectedSides(req)
	if buildFailed != "" {
		res.Status = StatusCheckFailed
		res.FailureReason = buildFailed
		res.RepairAvailable = false
		res.RepairScope = RepairScopeNone
		return res
	}

	// Labels.
	skip := labelSkipSet(req.Live)
	res.Differences = append(res.Differences, compareLabels(expectedLabels, req.Live.Labels, req.Policy.Scope, skip, &res.CheckedFieldCount)...)

	// Everything below is only Sharko's on a connection Sharko owns. On a
	// guest connection Sharko manages the addon labels and nothing else, so
	// the identity, the type and the connection data are not compared at all
	// — not reported as differences, and not reported as unchecked either,
	// because they are not inside this connection's scope in the first place.
	if req.Policy.Scope == ScopeFull || req.Policy.Scope == ScopeLimited {
		res.Differences = append(res.Differences, compareIdentityAndType(req, expectedSecret, &res.CheckedFieldCount)...)
		diffs, notChecked := compareConnectionData(req, expectedSecret, &res.CheckedFieldCount)
		res.Differences = append(res.Differences, diffs...)
		res.NotChecked = append(res.NotChecked, notChecked...)
	}

	sortDifferences(res.Differences)

	// The answer.
	//
	// A difference beats everything below it: out_of_sync is out_of_sync at
	// every scope. Otherwise, synced needs BOTH nothing wrong AND a full
	// scope — a narrower scope that found nothing wrong reports limited,
	// because "I found no problem in the part I looked at" is not "this is
	// right".
	switch {
	case len(res.Differences) > 0:
		res.Status = StatusOutOfSync
	case req.Policy.Scope == ScopeFull && len(res.NotChecked) == 0:
		res.Status = StatusSynced
	default:
		res.Status = StatusLimited
	}

	// R3-8: Repair offer computed AFTER the status is known. Only out_of_sync,
	// synced and limited reach here; the early exits (check_failed, missing,
	// ownership_conflict) already set repair_available: false above.
	res.RepairAvailable = req.Policy.RepairOffered()
	res.RepairScope = req.Policy.RepairScope

	return res
}

// expectedSides builds the expected label set and, where the mode allows it,
// the expected Secret. Returns a fixed safe sentence when the build failed.
//
// The expected Secret always comes from argosecrets.BuildClusterSecret. The
// expected labels come from the same builder on the owned-connection path, so
// the label set the comparison checks against is byte-for-byte the label set a
// write would produce.
func expectedSides(req Request) (labels map[string]string, secret *corev1.Secret, failure string) {
	// Guest connections: Sharko writes only the addon labels, so that is the
	// whole expectation. SyncLabelsOnly is the writer, and it deliberately
	// does not add the ownership or secret-type labels and strips the
	// connectivity-check label — so the expected set is the desired addon
	// labels with the check label removed, exactly as that writer computes
	// it.
	if req.Policy.Scope == ScopeAddonLabelsOnly {
		guest := make(map[string]string, len(req.DesiredAddonLabels))
		for k, v := range req.DesiredAddonLabels {
			guest[k] = v
		}
		models.RemoveConnectivityCheckLabels(guest)
		return guest, nil, ""
	}

	// Owned connections. The label set is what the write path would stamp:
	// the desired addon labels, plus the derived connectivity-check label
	// under the same live setting the writer reads, plus the two system
	// labels the builder adds last.
	specLabels := make(map[string]string, len(req.DesiredAddonLabels)+2)
	for k, v := range req.DesiredAddonLabels {
		specLabels[k] = v
	}
	models.ApplyConnectivityCheckLabel(specLabels, req.ConnectivityCheckOn)

	if req.ExpectedSpec == nil {
		// No independent copy of the credentials. The labels are still fully
		// knowable from git, so they are still compared; the credential
		// fields become "not checked". Build the label set through the
		// canonical builder's own label function so it matches a real write.
		return argosecrets.BuildClusterSecretLabels(argosecrets.ClusterSecretSpec{Labels: specLabels}), nil, ""
	}

	spec := *req.ExpectedSpec
	spec.Name = req.ClusterName
	spec.Labels = specLabels
	// Provenance annotations are deliberately not set: every one of them
	// changes on every write, so there is nothing to compare (see fields.go).
	spec.Annotations = nil

	built, err := argosecrets.BuildClusterSecret(spec, req.Namespace)
	if err != nil {
		// The error could name the cluster and, in some future shape, could
		// carry more. It is not passed through: a fixed sentence goes out and
		// the caller logs the detail on the server side.
		return nil, nil, "Sharko could not work out what this cluster's connection should look like, so there is nothing to compare against. Check again in a moment."
	}
	return built.Labels, built, ""
}

// compareLabels compares the label keys Sharko owns at this scope.
func compareLabels(expected, live map[string]string, scope Scope, skip map[string]bool, checked *int) []Difference {
	var diffs []Difference

	for key, want := range expected {
		if skip[key] || !ownedLabelKey(key, scope) {
			continue
		}
		*checked++
		have, ok := live[key]
		switch {
		case !ok:
			diffs = append(diffs, safeDifference(labelFieldPath(key), FieldMissing, strPtr(want), nil))
		case have != want:
			diffs = append(diffs, safeDifference(labelFieldPath(key), FieldDifferent, strPtr(want), strPtr(have)))
		}
	}

	for key, have := range live {
		if skip[key] || !ownedLabelKey(key, scope) {
			continue
		}
		if _, declared := expected[key]; declared {
			continue // already handled above
		}
		*checked++
		diffs = append(diffs, safeDifference(labelFieldPath(key), FieldUnexpected, nil, strPtr(have)))
	}

	return diffs
}

// compareIdentityAndType compares the Secret's name, namespace and type.
//
// These can only differ in odd circumstances — the Secret was fetched by name
// in a namespace, so the identity matches by construction — but the type
// genuinely can be wrong if somebody recreated the Secret by hand, and ArgoCD
// needs it Opaque.
func compareIdentityAndType(req Request, expectedSecret *corev1.Secret, checked *int) []Difference {
	var diffs []Difference

	*checked++
	if req.Live.Name != req.ClusterName {
		diffs = append(diffs, safeDifference(FieldPathName, FieldDifferent, strPtr(req.ClusterName), strPtr(req.Live.Name)))
	}

	*checked++
	if req.Live.Namespace != req.Namespace {
		diffs = append(diffs, safeDifference(FieldPathNamespace, FieldDifferent, strPtr(req.Namespace), strPtr(req.Live.Namespace)))
	}

	// The expected type is Opaque whether or not a full expected Secret could
	// be built — BuildClusterSecret always writes Opaque, and ArgoCD requires
	// it, so it is knowable without any credential material.
	wantType := string(corev1.SecretTypeOpaque)
	if expectedSecret != nil {
		wantType = string(expectedSecret.Type)
	}
	*checked++
	if string(req.Live.Type) != wantType {
		diffs = append(diffs, safeDifference(FieldPathSecretType, FieldDifferent, strPtr(wantType), strPtr(string(req.Live.Type))))
	}

	return diffs
}

// compareConnectionData compares the three data keys Sharko writes.
//
// data.name and data.server are the cluster's own name and API address. Both
// are already returned by the cluster list and connection detail endpoints, so
// showing them here reveals nothing new and makes a real difference actionable
// — the user can see the address is wrong.
//
// data.config is the credential blob. It is compared in memory, with a
// constant-time compare so the comparison itself does not become a way to
// learn about the value, and NEITHER SIDE comes back. All that leaves the
// server is the field path, one of same / different / missing / unexpected,
// and the fact that it is sensitive.
func compareConnectionData(req Request, expectedSecret *corev1.Secret, checked *int) ([]Difference, []NotCheckedField) {
	var diffs []Difference
	var notChecked []NotCheckedField

	// Kubernetes returns values in Data even for keys written through
	// StringData, so the live side always reads from Data.
	liveData := req.Live.Data

	// data.name and data.server: knowable from git and the backend, and
	// knowable even without a credential rebuild when the expected Secret is
	// absent — but only the builder's own output is trusted as expected, so
	// without it these are reported unchecked rather than guessed.
	if expectedSecret == nil {
		notChecked = append(notChecked,
			NotCheckedField{Path: FieldPathDataName, Reason: reasonNoIndependentCopy},
			NotCheckedField{Path: FieldPathDataServer, Reason: reasonNoIndependentCopy},
			NotCheckedField{Path: FieldPathDataConfig, Reason: reasonNoIndependentCopy},
		)
		return diffs, notChecked
	}

	for _, pair := range []struct {
		key  string
		path string
	}{
		{dataKeyName, FieldPathDataName},
		{dataKeyServer, FieldPathDataServer},
	} {
		want, wantOK := expectedSecret.StringData[pair.key]
		haveRaw, haveOK := liveData[pair.key]
		have := string(haveRaw)
		if !wantOK && !haveOK {
			continue
		}
		*checked++
		switch {
		case !wantOK:
			diffs = append(diffs, safeDifference(pair.path, FieldUnexpected, nil, strPtr(have)))
		case !haveOK:
			diffs = append(diffs, safeDifference(pair.path, FieldMissing, strPtr(want), nil))
		case have != want:
			diffs = append(diffs, safeDifference(pair.path, FieldDifferent, strPtr(want), strPtr(have)))
		}
	}

	// data.config, the credential blob.
	if req.Policy.Mode == ModeEKSToken {
		// The configured credentials source stores EKS cluster metadata, not a
		// credential — so there is no credential on the expected side and
		// nothing to compare the live blob against. Calling it different would
		// report drift that is not real; claiming it matched would be a lie
		// about a check that never happened. So it is not checked, and the
		// reason says so in plain words.
		//
		// Nothing here creates a credential to compare with, either: the
		// expected side comes from providers.StoredConnectionFacts, which stops
		// at the stored payload. A WRITE creates a token, once. A check creates
		// none.
		notChecked = append(notChecked, NotCheckedField{Path: FieldPathDataConfig, Reason: reasonFreshTokenEveryFetch})
		return diffs, notChecked
	}

	wantConfig, wantOK := expectedSecret.StringData[dataKeyConfig]
	haveConfig, haveOK := liveData[dataKeyConfig]
	if wantOK || haveOK {
		*checked++
		switch {
		case !wantOK:
			diffs = append(diffs, sensitiveDifference(FieldPathDataConfig, FieldUnexpected))
		case !haveOK:
			diffs = append(diffs, sensitiveDifference(FieldPathDataConfig, FieldMissing))
		case subtle.ConstantTimeCompare([]byte(wantConfig), haveConfig) != 1:
			diffs = append(diffs, sensitiveDifference(FieldPathDataConfig, FieldDifferent))
		}
	}

	// Data keys nobody at Sharko wrote are not compared and not reported.
	// They belong to whoever put them there, they are preserved by every
	// write path, and naming them here would be reporting on somebody else's
	// data.

	return diffs, notChecked
}
