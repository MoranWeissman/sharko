package argosecrets

// EKS token renewal (ep4 serious 1).
//
// An eks-token connection is written with a bearer token Sharko makes at
// write time. That token lasts 15 minutes. Before this file, nothing ever
// replaced it on a Secret that already existed, so ArgoCD lost the cluster a
// quarter of an hour after every write.
//
// This file holds the two pieces the reconciler needs and that only this
// package can do safely, because only this package knows the config shape:
//
//   - EKSBearerTokenFacts reads a live config and says whether it is exactly
//     the bearerToken shape Sharko writes, carrying an EKS token, and when
//     that token was made. It reads only; it returns no error values, so no
//     caller can ever log text that came from inside a token.
//   - Manager.RenewOwnedBearerToken swaps the config key, and nothing else, on
//     a Secret Sharko owns, if the config is still exactly the one the caller
//     checked.

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EKSTokenPrefix is the fixed start of every EKS sign-in token.
const EKSTokenPrefix = "k8s-aws-v1."

// eksTokenDateLayout is the X-Amz-Date layout inside the signed STS address.
const eksTokenDateLayout = "20060102T150405Z"

// ErrRenewalConfigChanged means the live config was no longer the one the
// caller checked before it made a new token, so nothing was written.
var ErrRenewalConfigChanged = errors.New("this cluster's ArgoCD connection changed while Sharko was renewing its token, so Sharko wrote nothing")

// EKSBearerTokenFacts reads a live data.config value.
//
// token is the bearer token, set only when shapeOK is true. shapeOK is true
// only when the config is exactly what Sharko's own builder writes for a
// bearer token (re-marshalling it gives back the same bytes) and the token is
// an EKS token. issuedAt / dateOK say when the token was made, read from the
// X-Amz-Date inside it. dateOK false with shapeOK true means "an EKS token
// whose date can't be read" — the caller treats that as old.
//
// Nothing here returns or logs an error: a parse failure only ever comes
// back as a false.
func EKSBearerTokenFacts(config []byte) (token string, shapeOK bool, issuedAt time.Time, dateOK bool) {
	var cfg bearerTokenConfig
	dec := json.NewDecoder(bytes.NewReader(config))
	dec.DisallowUnknownFields()
	if dec.Decode(&cfg) != nil {
		return "", false, time.Time{}, false
	}
	if !strings.HasPrefix(cfg.BearerToken, EKSTokenPrefix) {
		return "", false, time.Time{}, false
	}
	again, err := json.MarshalIndent(cfg, "", "  ") // #nosec G117 -- re-marshal only to compare bytes; never logged or returned
	if err != nil || !bytes.Equal(again, config) {
		return "", false, time.Time{}, false
	}
	issuedAt, dateOK = eksTokenIssuedAt(cfg.BearerToken)
	return cfg.BearerToken, true, issuedAt, dateOK
}

// eksTokenIssuedAt decodes the signed STS address inside an EKS token and
// reads its X-Amz-Date. Errors are dropped on purpose (see above).
func eksTokenIssuedAt(token string) (time.Time, bool) {
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(token, EKSTokenPrefix))
	if err != nil {
		return time.Time{}, false
	}
	u, err := url.Parse(string(raw))
	if err != nil {
		return time.Time{}, false
	}
	t, err := time.Parse(eksTokenDateLayout, u.Query().Get("X-Amz-Date"))
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

// RenewOwnedBearerToken writes newConfig into data.config of the named
// cluster Secret, and changes nothing else.
//
// It re-reads the Secret and refuses unless it is still labelled as Sharko's
// (exactly), is not adopted, and its config is still byte-for-byte
// expectedConfig. The Update carries the resourceVersion from that read, so a
// change made by anyone in between makes it fail instead of overwrite.
//
// Both config values must come from BuildSecretConfigJSON.
func (m *Manager) RenewOwnedBearerToken(ctx context.Context, name, expectedConfig, newConfig string) error {
	existing, getErr := m.getClusterSecret(ctx, name)
	if apierrors.IsNotFound(getErr) {
		return ErrRepairSecretMissing
	}
	if getErr != nil {
		return getErr
	}
	if existing.Labels[LabelManagedBy] != ManagedByValue || IsAdopted(existing.Annotations) {
		return ErrRepairOwnershipChanged
	}
	if string(existing.Data[dataKeyConfig]) != expectedConfig {
		return ErrRenewalConfigChanged
	}
	updated := existing.DeepCopy()
	updated.Data[dataKeyConfig] = []byte(newConfig)
	updated.StringData = nil
	if _, err := m.client.CoreV1().Secrets(m.namespace).Update(ctx, updated, metav1.UpdateOptions{}); err != nil {
		if apierrors.IsConflict(err) {
			return ErrRepairSecretChangedUnderneath
		}
		return err
	}
	return nil
}
