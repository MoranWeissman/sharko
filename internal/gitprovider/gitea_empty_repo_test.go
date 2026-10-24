package gitprovider

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
)

// fakeEmptyGitea answers the way Gitea 1.22 does for a repo with no commit:
// branch calls say "Git Repository is empty." (status 404 on 1.22), and the
// contents API accepts a first file and creates the base branch with it.
type fakeEmptyGitea struct {
	mu            sync.Mutex
	emptyStatus   int
	hasCommit     bool
	branches      map[string]bool
	createdFiles  []string
	createdBranch []string
	filePutBranch string
	fileAttempts  int
}

func newFakeEmptyGitea(emptyStatus int) *fakeEmptyGitea {
	return &fakeEmptyGitea{emptyStatus: emptyStatus, branches: map[string]bool{}}
}

func (f *fakeEmptyGitea) handler(t *testing.T) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		p := r.URL.Path
		switch {
		case p == "/api/v1/version":
			w.Write([]byte(`{"version":"1.22.6"}`))
		case r.Method == "POST" && p == "/api/v1/repos/o/r/branches":
			var body struct {
				New string `json:"new_branch_name"`
				Old string `json:"old_branch_name"`
			}
			json.NewDecoder(r.Body).Decode(&body)
			if !f.hasCommit {
				w.WriteHeader(f.emptyStatus)
				w.Write([]byte(`{"message":"Git Repository is empty.","url":"http://gitea/api/swagger"}`))
				return
			}
			if !f.branches[body.Old] {
				w.WriteHeader(404)
				w.Write([]byte(`{"message":"The target couldn't be found.","errors":["branch does not exist [name: ` + body.Old + `]"]}`))
				return
			}
			f.branches[body.New] = true
			f.createdBranch = append(f.createdBranch, body.New)
			w.WriteHeader(201)
			w.Write([]byte(`{"name":"` + body.New + `","commit":{"id":"abc"}}`))
		case r.Method == "GET" && strings.HasPrefix(p, "/api/v1/repos/o/r/contents/"):
			w.WriteHeader(404)
			w.Write([]byte(`{"message":"Not Found"}`))
		case r.Method == "POST" && strings.HasPrefix(p, "/api/v1/repos/o/r/contents/"):
			var body struct {
				Branch string `json:"branch"`
			}
			raw, _ := io.ReadAll(r.Body)
			json.Unmarshal(raw, &body)
			file := strings.TrimPrefix(p, "/api/v1/repos/o/r/contents/")
			f.fileAttempts++
			if !f.hasCommit {
				// First file on an empty repo makes the base branch.
				f.hasCommit = true
				b := body.Branch
				if b == "" {
					b = "main"
				}
				f.branches[b] = true
				f.filePutBranch = b
			} else if !f.branches[body.Branch] {
				w.WriteHeader(404)
				w.Write([]byte(`{"message":"branch does not exist"}`))
				return
			}
			f.createdFiles = append(f.createdFiles, file+"@"+body.Branch)
			w.WriteHeader(201)
			w.Write([]byte(`{"content":{"name":"x","path":"` + file + `","sha":"s"},"commit":{"sha":"c"}}`))
		default:
			t.Errorf("unexpected request %s %s", r.Method, p)
			w.WriteHeader(500)
		}
	})
}

func TestGiteaCreateBranchOnEmptyRepo(t *testing.T) {
	for _, status := range []int{http.StatusNotFound, http.StatusConflict} {
		fake := newFakeEmptyGitea(status)
		srv := httptest.NewServer(fake.handler(t))
		p, err := NewGiteaProvider(srv.URL, "o", "r", "tok")
		if err != nil {
			t.Fatal(err)
		}
		ctx := context.Background()
		if err := p.CreateBranch(ctx, "sharko/initialize-repository-1", "main"); err != nil {
			t.Fatalf("status %d: CreateBranch on an empty repo failed: %v", status, err)
		}
		if len(fake.createdFiles) != 1 || fake.createdFiles[0] != "README.md@main" {
			t.Errorf("status %d: want one README.md written on main, got %v", status, fake.createdFiles)
		}
		if len(fake.createdBranch) != 1 || fake.createdBranch[0] != "sharko/initialize-repository-1" {
			t.Errorf("status %d: new branch not created after the first commit: %v", status, fake.createdBranch)
		}
		// The bootstrap flow then writes its files on the new branch.
		if err := p.BatchCreateFiles(ctx, map[string][]byte{"a.yaml": []byte("a")}, "sharko/initialize-repository-1", "msg"); err != nil {
			t.Fatalf("status %d: writing bootstrap files failed: %v", status, err)
		}
		srv.Close()
	}
}

// A missing base branch on a repo that already has commits is NOT an empty
// repo: it must stay an error and must not write a README.
func TestGiteaCreateBranchMissingBaseIsNotEmptyRepo(t *testing.T) {
	fake := newFakeEmptyGitea(http.StatusNotFound)
	fake.hasCommit = true
	fake.branches["develop"] = true
	srv := httptest.NewServer(fake.handler(t))
	defer srv.Close()
	p, _ := NewGiteaProvider(srv.URL, "o", "r", "tok")
	err := p.CreateBranch(context.Background(), "feature", "main")
	if err == nil {
		t.Fatal("expected an error for a missing base branch on a populated repo")
	}
	if fake.fileAttempts != 0 {
		t.Errorf("no README write may even be tried on a populated repo, got %d tries", fake.fileAttempts)
	}
}

// Live check against a real Gitea, skipped unless SHARKO_T1_GITEA_URL is set.
// Needs SHARKO_T1_GITEA_OWNER, _REPO (an EMPTY repo) and _TOKEN.
func TestGiteaCreateBranchOnEmptyRepoLive(t *testing.T) {
	url := os.Getenv("SHARKO_T1_GITEA_URL")
	if url == "" {
		t.Skip("SHARKO_T1_GITEA_URL not set")
	}
	p, err := NewGiteaProvider(url, os.Getenv("SHARKO_T1_GITEA_OWNER"), os.Getenv("SHARKO_T1_GITEA_REPO"), os.Getenv("SHARKO_T1_GITEA_TOKEN"))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := p.CreateBranch(ctx, "sharko/initialize-repository-live", "main"); err != nil {
		t.Fatalf("CreateBranch: %v", err)
	}
	if err := p.BatchCreateFiles(ctx, map[string][]byte{"catalog.yaml": []byte("x: 1\n")}, "sharko/initialize-repository-live", "init"); err != nil {
		t.Fatalf("BatchCreateFiles: %v", err)
	}
	pr, err := p.CreatePullRequest(ctx, "init", "body", "sharko/initialize-repository-live", "main")
	if err != nil {
		t.Fatalf("CreatePullRequest: %v", err)
	}
	t.Logf("opened PR %d", pr.ID)
}
