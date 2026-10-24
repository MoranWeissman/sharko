package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/MoranWeissman/sharko/internal/config"
	"github.com/MoranWeissman/sharko/internal/models"
)

// EP3 dry run round R, R9: an address Sharko cannot read is never repeated
// back, because a token written into it would come back to the browser.

const roundRToken = "ghp_ROUNDRSECRET123"

var unreadableAddresses = []string{
	"x:" + roundRToken + "@gitea.local/org/repo", // no "//": url.Parse reads "x" as the scheme
	"https://user:" + roundRToken + "@git.local/%zz",
}

func TestTestCredentials_UnreadableAddress_NoProvider_SaysTheAddress(t *testing.T) {
	svc := NewConnectionService(config.NewFileStore(t.TempDir() + "/c.yaml"))
	for _, addr := range unreadableAddresses {
		gitErr, _, _ := svc.TestCredentials(context.Background(), &models.Connection{
			Git: models.GitRepoConfig{RepoURL: addr},
		})
		if gitErr == nil || gitErr.Error() != UnreadableGitHostSentence {
			t.Fatalf("%s: got %v, want %q", addr, gitErr, UnreadableGitHostSentence)
		}
	}
	const want = "Sharko cannot read the Git host in the address. Check that it looks like https://host/owner/repo, then test again."
	if UnreadableGitHostSentence != want {
		t.Fatalf("sentence = %q, want %q", UnreadableGitHostSentence, want)
	}
}

func TestTestCredentials_UnreadableAddress_WithProvider_NeverEchoesIt(t *testing.T) {
	svc := NewConnectionService(config.NewFileStore(t.TempDir() + "/c.yaml"))
	gitErr, _, _ := svc.TestCredentials(context.Background(), &models.Connection{
		Git: models.GitRepoConfig{Provider: models.GitProviderGitea, RepoURL: unreadableAddresses[1]},
	})
	if gitErr == nil || gitErr.Error() != models.UnreadableRepoAddressSentence {
		t.Fatalf("got %v, want %q", gitErr, models.UnreadableRepoAddressSentence)
	}
	const want = "Sharko cannot read the Git repository address. Check that it looks like https://host/owner/repo."
	if models.UnreadableRepoAddressSentence != want {
		t.Fatalf("sentence = %q, want %q", models.UnreadableRepoAddressSentence, want)
	}
}

func TestCreateConnection_UnreadableAddress_NeverEchoesIt(t *testing.T) {
	svc := NewConnectionService(config.NewFileStore(t.TempDir() + "/c.yaml"))
	for _, provider := range []models.GitProviderType{"", models.GitProviderGitea} {
		err := svc.Create(models.CreateConnectionRequest{
			Name: "x",
			Git:  models.GitRepoConfig{Provider: provider, RepoURL: unreadableAddresses[1], Owner: "o", Repo: "r"},
		})
		if err == nil {
			t.Fatalf("provider %q: want an error", provider)
		}
		if strings.Contains(err.Error(), roundRToken) || strings.Contains(err.Error(), "%zz") {
			t.Fatalf("provider %q: the address came back in the error: %q", provider, err.Error())
		}
		// EP3 dry run round S, S40: the sentence alone, pinned exactly.
		if err.Error() != models.UnreadableRepoAddressSentence {
			t.Fatalf("provider %q: got %q, want exactly %q", provider, err.Error(), models.UnreadableRepoAddressSentence)
		}
		if !errors.Is(err, ErrValidation) {
			t.Fatalf("provider %q: %v is not a validation error (the API would not answer 400)", provider, err)
		}
	}
}

// A readable but unknown host is still named: that part has no token.
func TestUnrecognisedGitHost_ReadableHostIsNamedWithoutTheUserPart(t *testing.T) {
	svc := NewConnectionService(config.NewFileStore(t.TempDir() + "/c.yaml"))
	gitErr, _, _ := svc.TestCredentials(context.Background(), &models.Connection{
		Git: models.GitRepoConfig{RepoURL: "https://user:" + roundRToken + "@gitea.example.test:3000/acme/platform"},
	})
	const want = `Sharko does not recognise the Git host "gitea.example.test". Choose the Git provider (GitHub or Gitea) next to the address, then test again.`
	if gitErr == nil || gitErr.Error() != want {
		t.Fatalf("got %v, want %q", gitErr, want)
	}
}
