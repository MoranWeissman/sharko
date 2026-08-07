# Changelog

This file only tracks unreleased changes. The full per-version release
history lives in one place:
[docs/site/release-notes.md](docs/site/release-notes.md), also published at
[https://sharko.readthedocs.io/en/latest/release-notes/](https://sharko.readthedocs.io/en/latest/release-notes/).

## [Unreleased]

### Removed

- The shelved `ClusterAddons` Kubernetes operator (CRD + controller) is out
  of the build. The code is preserved on the `operator-shelf` branch, not
  deleted. Why: the v4 direction is the engine chart, and a CRD whose spec
  doesn't drive anything yet was eroding trust in what Sharko ships. The
  cluster reconciler (`internal/clusterreconciler`) is unaffected — it stays
  the one writer of ArgoCD cluster Secret addon labels. Local playground
  targets `make operator-playground-*` were renamed to `make playground-*`
  (old names still work as aliases).
