package orchestrator

import (
	"github.com/MoranWeissman/sharko/internal/credsafe"
	"github.com/MoranWeissman/sharko/internal/models"
)

// catalog_repo_url_door.go — the early, friendly refusal for the v3 addon
// doors.
//
// The guarantee that a credential-bearing repository address never reaches Git
// lives in internal/config's two canonical writers, and the v4 doors get their
// early refusal from catalog.ValidateCatalogEntry. The v3 doors — AddAddon and
// ConfigureAddon — had no per-entry gate of their own, so without this an
// operator would only find out after Sharko had already read the repository
// and built a branch name, and the message would arrive wrapped in whatever
// the writer said.
//
// Every function here calls credsafe.ValidateSupportedRepoURL. None of them
// contains a rule.

// checkAddonSourceRepoURLs returns the first refusal among a list of extra
// chart sources, naming which one by position.
func checkAddonSourceRepoURLs(field string, sources []models.AddonSource) error {
	for i, s := range sources {
		if err := credsafe.ValidateSupportedRepoURLAt("", fieldIndex(field, i), s.RepoURL); err != nil {
			return err
		}
	}
	return nil
}

// fieldIndex renders "additional_sources[2].repo_url" without pulling fmt in
// for one string.
func fieldIndex(field string, i int) string {
	return field + "[" + itoa(i) + "].repo_url"
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b [20]byte
	pos := len(b)
	for i > 0 {
		pos--
		b[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(b[pos:])
}

// checkAddAddonRepoURLs covers every repository address POST /api/v1/addons
// could put into the v3 catalog file.
func checkAddAddonRepoURLs(req AddAddonRequest) error {
	if err := credsafe.ValidateSupportedRepoURLAt("", "repo_url", req.RepoURL); err != nil {
		return err
	}
	return checkAddonSourceRepoURLs("additional_sources", req.AdditionalSources)
}

// checkConfigureAddonRepoURLs covers every repository address
// PATCH /api/v1/addons/{name} could put into the v3 catalog file. The request
// has no top-level chart repository of its own — only the extra sources carry
// one.
func checkConfigureAddonRepoURLs(req ConfigureAddonRequest) error {
	return checkAddonSourceRepoURLs("additional_sources", req.AdditionalSources)
}
