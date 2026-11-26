package notifications

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/MoranWeissman/sharko/internal/logging"
)

// VersionInfo contains addon version data used by the Checker.
type VersionInfo struct {
	AddonName       string
	CatalogVersion  string
	ClusterVersions map[string]string // cluster name -> deployed version
	LatestVersion   string            // from Helm repo
}

// VersionProvider is the interface the Checker consults each cycle.
type VersionProvider interface {
	GetVersionInfo(ctx context.Context) ([]VersionInfo, error)
}

// Checker runs periodically and pushes notifications into the Store based on
// version drift and available upgrades detected via a VersionProvider.
type Checker struct {
	store    *Store
	provider VersionProvider
	interval time.Duration
	stopCh   chan struct{}
	stopOnce sync.Once
}

// NewChecker creates a Checker that is not yet running.
func NewChecker(store *Store, provider VersionProvider, interval time.Duration) *Checker {
	return &Checker{
		store:    store,
		provider: provider,
		interval: interval,
		stopCh:   make(chan struct{}),
	}
}

// Start launches the background goroutine. It runs one check immediately, then
// repeats on the configured interval until Stop is called.
func (c *Checker) Start() {
	go func() {
		// Run immediately on start.
		c.check()

		ticker := time.NewTicker(c.interval)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				c.check()
			case <-c.stopCh:
				return
			}
		}
	}()
}

// Stop signals the background goroutine to exit. It is safe to call Stop
// multiple times; the channel is closed exactly once.
func (c *Checker) Stop() {
	c.stopOnce.Do(func() {
		close(c.stopCh)
	})
}

func (c *Checker) check() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	ctx = logging.WithRequestID(ctx, fmt.Sprintf("notifcheck-%d", time.Now().Unix()))

	infos, err := c.provider.GetVersionInfo(ctx)
	if err != nil {
		logging.LoggerFromContext(ctx).Error("notification check failed", "error", err, "component", "notifications")
		return
	}

	// No answer at all (no Git or ArgoCD connection yet) is not "there are
	// no addons". Nothing is raised and nothing is cleared.
	if infos == nil {
		return
	}

	now := time.Now()

	// live collects the ID of every addon note that is still true this
	// round. Whatever addon note is NOT in it afterwards is about something
	// that is no longer so — an upgrade that was done, a cluster that caught
	// up, an addon that was removed — and is cleared (pre-release walk 4:
	// five notes stayed on the bell after the addons were gone, and drift
	// notes kept naming a version that was rolled back).
	live := map[string]bool{}
	// unknownLatest holds addons whose newest version could not be read
	// this round. Their upgrade notes are left alone: "could not check" is
	// not "no longer true".
	var unknownLatest []string
	raise := func(code Code, p Params) {
		n := New(code, "", p, now)
		live[n.ID] = true
		c.store.Add(n)
	}

	for _, info := range infos {
		// The checker names the SUBJECT of each alert — which addon, which
		// cluster, which versions — and nothing else. It writes no sentence
		// and no identifier: New() hands these to the checks in render.go and
		// the server composes the words. A version or a name that is not the
		// shape it claims to be is not interpolated at all.

		if info.LatestVersion == "" {
			unknownLatest = append(unknownLatest, info.AddonName)
		}

		// Check for upgrades (major/minor only).
		if info.LatestVersion != "" && info.LatestVersion != info.CatalogVersion {
			if isMajorOrMinorUpgrade(info.CatalogVersion, info.LatestVersion) {
				raise(CodeAddonUpgradeAvailable, Params{
					Addon:          info.AddonName,
					Version:        info.LatestVersion,
					CatalogVersion: info.CatalogVersion,
				})
			}

			// Check for security-relevant versions: flag major version bumps as
			// potentially security-relevant since they often include breaking CVE fixes.
			if isMajorUpgrade(info.CatalogVersion, info.LatestVersion) {
				raise(CodeAddonMajorUpdate, Params{
					Addon:          info.AddonName,
					Version:        info.LatestVersion,
					CatalogVersion: info.CatalogVersion,
				})
			}
		}

		// Check for drift between cluster version and catalog version.
		for clusterName, clusterVersion := range info.ClusterVersions {
			if clusterVersion != info.CatalogVersion && clusterVersion != "" {
				raise(CodeAddonVersionDrift, Params{
					Addon:          info.AddonName,
					Cluster:        clusterName,
					Version:        clusterVersion,
					CatalogVersion: info.CatalogVersion,
				})
			}
		}
	}

	c.store.ResolveExcept(
		[]Code{CodeAddonUpgradeAvailable, CodeAddonMajorUpdate, CodeAddonVersionDrift},
		func(n Notification) bool {
			if live[n.ID] {
				return true
			}
			if n.Code == CodeAddonVersionDrift {
				return false
			}
			for _, addon := range unknownLatest {
				if strings.HasPrefix(n.ID, "upgrade-"+addon+"-") || strings.HasPrefix(n.ID, "security-"+addon+"-") {
					return true
				}
			}
			return false
		})
}

// isMajorOrMinorUpgrade returns true when the latest version differs from
// current in the major or minor component (patch-only bumps are ignored).
func isMajorOrMinorUpgrade(current, latest string) bool {
	curParts := parseSemverParts(current)
	latParts := parseSemverParts(latest)
	if len(curParts) < 2 || len(latParts) < 2 {
		return current != latest
	}
	return curParts[0] != latParts[0] || curParts[1] != latParts[1]
}

// isMajorUpgrade returns true when the latest version has a higher major
// component than the current version. Major bumps often include security fixes.
func isMajorUpgrade(current, latest string) bool {
	curParts := parseSemverParts(current)
	latParts := parseSemverParts(latest)
	if len(curParts) < 1 || len(latParts) < 1 {
		return false
	}
	return latParts[0] > curParts[0]
}

// parseSemverParts parses a semver string into its numeric components.
// It strips a leading "v" and any pre-release suffix (e.g. "-beta.1").
func parseSemverParts(v string) []int {
	v = strings.TrimPrefix(v, "v")
	parts := strings.SplitN(v, ".", 3)
	result := make([]int, 0, 3)
	for _, p := range parts {
		p = strings.Split(p, "-")[0] // strip pre-release
		n, err := strconv.Atoi(p)
		if err != nil {
			break
		}
		result = append(result, n)
	}
	return result
}
