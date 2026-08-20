package git_repo

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/go-git/go-git/v5"
	gitconfig "github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/plumbing/transport"
	gogitclient "github.com/go-git/go-git/v5/plumbing/transport/client"
	http "github.com/go-git/go-git/v5/plumbing/transport/http"
	"github.com/go-git/go-git/v5/storage/memory"
	"github.com/go-git/go-git/v5/utils/merkletrie"

	"github.com/Tencent/WeKnora/internal/datasource"
)

// errHistoryRewritten marks a previous cursor commit that no longer exists in
// the fetched history (force-push / history rewrite / shallow boundary). The
// caller falls back to a full re-scan instead of failing the sync.
var errHistoryRewritten = errors.New(
	"git_repo: previous cursor commit not found (history rewritten); falling back to full rescan")

// errEmptyRemote marks a remote with no resolvable branch.
var errEmptyRemote = errors.New("git_repo: remote has no resolvable branch")

const (
	// localStorageBaseEnv is the shared local file-storage root. git_repo
	// clones live under <LOCAL_STORAGE_BASE_DIR>/git-repos, namespaced per
	// tenant / data source by cloneDirFor. The earlier dedicated
	// GIT_REPO_STORAGE_BASE_DIR override was dropped per review: one storage
	// root is enough and per-tenant/datasource isolation already prevents
	// cross-tenant collisions.
	localStorageBaseEnv = "LOCAL_STORAGE_BASE_DIR"
)

// client wraps go-git repository operations. It is stateless apart from the
// per-data-source identity used to namespace clone directories on disk.
type client struct {
	dsID     string
	tenantID uint64
	token    string
	baseDir  string
}

func newClient(dsID string, tenantID uint64, token string) *client {
	ensureSSRFTransport()
	return &client{dsID: dsID, tenantID: tenantID, token: token, baseDir: repoStorageBase()}
}

// ensureSSRFTransport registers SSRF-guarded HTTP transports for go-git's
// http/https protocols (clone, fetch, and ls-remote all resolve through the
// transport registry). Using datasource.NewConnectorHTTPClient reuses the same
// dial-time + redirect SSRF protection every other connector gets, so a repo_url
// that passed URL-level validation cannot be re-pointed at an internal target
// via DNS rebinding between validation and connection. Idempotent and process-
// wide, matching how go-git's transport registry works.
var (
	ensureSSRFTransportOnce sync.Once
)

func ensureSSRFTransport() {
	ensureSSRFTransportOnce.Do(func() {
		safeClient := datasource.NewConnectorHTTPClient(30 * time.Second)
		gogitclient.InstallProtocol("http", http.NewClient(safeClient))
		gogitclient.InstallProtocol("https", http.NewClient(safeClient))
	})
}

// repoStorageBase resolves the root under which git clones are kept. Clones
// land under LOCAL_STORAGE_BASE_DIR (the persistent data-files volume), which
// the appuser can write, and are namespaced per tenant / data source / repo by
// cloneDirFor.
func repoStorageBase() string {
	base := os.Getenv(localStorageBaseEnv)
	if base == "" {
		base = "/data/files"
	}
	return filepath.Join(base, "git-repos")
}

// cloneDirFor returns the on-disk clone directory for a repo+branch selection.
// Hashing the URL and branch means a config change (branch switch, URL edit)
// naturally re-clones without stale-dir collisions.
func (c *client) cloneDirFor(repoURL, branch string) string {
	key := repoURL + "\x00" + branch
	sum := sha1.Sum([]byte(key))
	return filepath.Join(c.baseDir, fmt.Sprint(c.tenantID), c.dsID, hex.EncodeToString(sum[:8]))
}

func buildAuth(token string) transport.AuthMethod {
	if token == "" {
		return nil
	}
	// "oauth2" as the username works for GitHub/GitLab/Gitea/Bitbucket https
	// remotes; plain basic auth also works for most self-hosted git servers.
	return &http.BasicAuth{Username: "oauth2", Password: token}
}

// lsRemoteRefs lists remote refs without writing anything to disk. It resolves
// the requested branch (or the remote default branch when branch is empty) and
// returns its head SHA plus the resolved branch name. Used by Validate and by
// default-branch resolution.
func (c *client) lsRemoteRefs(ctx context.Context, repoURL, branch string) (headSHA, resolvedBranch string, err error) {
	rem := git.NewRemote(memory.NewStorage(), &gitconfig.RemoteConfig{Name: "origin", URLs: []string{repoURL}})
	refs, err := rem.ListContext(ctx, &git.ListOptions{Auth: buildAuth(c.token)})
	if err != nil {
		return "", "", fmt.Errorf("git_repo: list remote refs %s: %w", repoURL, err)
	}

	heads := make(map[string]plumbing.Hash)
	var headSym *plumbing.Reference
	for _, ref := range refs {
		if ref.Name() == plumbing.HEAD {
			headSym = ref
		} else if ref.Name().IsBranch() {
			heads[ref.Name().Short()] = ref.Hash()
		}
	}
	if len(heads) == 0 {
		return "", "", errEmptyRemote
	}

	if branch != "" {
		h, ok := heads[branch]
		if !ok {
			return "", "", fmt.Errorf("git_repo: branch %q not found in remote %s", branch, repoURL)
		}
		return h.String(), branch, nil
	}

	// Resolve the default branch from the HEAD symref (e.g. refs/heads/main).
	if headSym != nil && headSym.Target().IsBranch() {
		name := headSym.Target().Short()
		if h, ok := heads[name]; ok {
			return h.String(), name, nil
		}
	}
	// Fallback: deterministic pick of the first branch by name.
	names := make([]string, 0, len(heads))
	for name := range heads {
		names = append(names, name)
	}
	sort.Strings(names)
	return heads[names[0]].String(), names[0], nil
}

// ensureCheckedOut makes sure dir contains the repository at the given branch,
// updated to its tip. First run clones; later runs fetch + hard-reset (which
// also absorbs force-pushes). Returns the open repository, the head SHA and the
// resolved branch name.
func (c *client) ensureCheckedOut(
	ctx context.Context, dir, repoURL, branch string,
) (*git.Repository, string, string, error) {
	repo, err := git.PlainOpen(dir)
	if err != nil {
		if !errors.Is(err, git.ErrRepositoryNotExists) && !os.IsNotExist(err) {
			return nil, "", "", fmt.Errorf("git_repo: open clone %s: %w", dir, err)
		}
		return c.clone(ctx, dir, repoURL, branch)
	}

	// Existing clone: fetch all branch tips, then hard-reset to the requested one.
	if err := repo.FetchContext(ctx, &git.FetchOptions{
		RefSpecs: []gitconfig.RefSpec{"+refs/heads/*:refs/remotes/origin/*"},
		Auth:     buildAuth(c.token),
		Force:    true,
	}); err != nil && !errors.Is(err, git.NoErrAlreadyUpToDate) {
		return nil, "", "", fmt.Errorf("git_repo: fetch %s: %w", repoURL, err)
	}

	resolvedBranch := branch
	if resolvedBranch == "" {
		resolvedBranch, err = defaultRemoteBranch(repo)
		if err != nil {
			return nil, "", "", err
		}
	}
	tipRef, err := repo.Reference(plumbing.NewRemoteReferenceName("origin", resolvedBranch), false)
	if err != nil {
		return nil, "", "", fmt.Errorf("git_repo: resolve remote branch %q: %w", resolvedBranch, err)
	}
	wt, err := repo.Worktree()
	if err != nil {
		return nil, "", "", err
	}
	if err := wt.Reset(&git.ResetOptions{Commit: tipRef.Hash(), Mode: git.HardReset}); err != nil {
		return nil, "", "", fmt.Errorf("git_repo: hard reset to %s: %w", tipRef.Hash().String(), err)
	}
	return repo, tipRef.Hash().String(), resolvedBranch, nil
}

func (c *client) clone(ctx context.Context, dir, repoURL, branch string) (*git.Repository, string, string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, "", "", err
	}
	opts := &git.CloneOptions{
		URL:          repoURL,
		Auth:         buildAuth(c.token),
		SingleBranch: true,
	}
	if branch != "" {
		opts.ReferenceName = plumbing.NewBranchReferenceName(branch)
	}
	repo, err := git.PlainCloneContext(ctx, dir, false, opts)
	if err != nil {
		return nil, "", "", fmt.Errorf("git_repo: clone %s: %w", repoURL, err)
	}
	head, err := repo.Head()
	if err != nil {
		return nil, "", "", fmt.Errorf("git_repo: resolve head of %s: %w", repoURL, err)
	}
	resolvedBranch := head.Name().Short()
	return repo, head.Hash().String(), resolvedBranch, nil
}

// defaultRemoteBranch resolves the default branch of an existing clone from the
// origin/HEAD symref (set up by go-git at clone time).
func defaultRemoteBranch(repo *git.Repository) (string, error) {
	ref, err := repo.Reference(plumbing.NewRemoteHEADReferenceName("origin"), false)
	if err != nil {
		return "", fmt.Errorf("git_repo: resolve default remote branch: %w", err)
	}
	name := strings.TrimPrefix(string(ref.Target()), "refs/remotes/origin/")
	if name == "" || name == string(ref.Target()) {
		return "", errEmptyRemote
	}
	return name, nil
}

// nameStatusChange is the git diff --name-status analog produced by
// diffNameStatus. OldPath/NewPath are repository-relative paths.
type nameStatusChange struct {
	OldPath string
	NewPath string
	Deleted bool // file removed
	Renamed bool // path changed (OldPath -> NewPath)
}

// diffNameStatus computes the name-status diff between two commits of an open
// repository. A missing previous commit (history rewrite) yields
// errHistoryRewritten.
func diffNameStatus(repo *git.Repository, prevSHA, headSHA string) ([]nameStatusChange, error) {
	prevCommit, err := repo.CommitObject(plumbing.NewHash(prevSHA))
	if err != nil {
		if errors.Is(err, plumbing.ErrObjectNotFound) {
			return nil, errHistoryRewritten
		}
		return nil, err
	}
	headCommit, err := repo.CommitObject(plumbing.NewHash(headSHA))
	if err != nil {
		return nil, err
	}
	prevTree, err := prevCommit.Tree()
	if err != nil {
		return nil, err
	}
	headTree, err := headCommit.Tree()
	if err != nil {
		return nil, err
	}
	changes, err := object.DiffTree(prevTree, headTree)
	if err != nil {
		return nil, err
	}

	out := make([]nameStatusChange, 0, len(changes))
	for _, ch := range changes {
		action, _ := ch.Action()
		// From/To are value ChangeEntry; for insertions From is the zero value and
		// for deletions To is the zero value (empty Name). go-git's tree diff does
		// not detect renames — a rename surfaces as Delete(old)+Insert(new).
		from := ch.From.Name
		to := ch.To.Name
		switch action {
		case merkletrie.Delete:
			out = append(out, nameStatusChange{OldPath: from, Deleted: true})
		case merkletrie.Insert:
			out = append(out, nameStatusChange{NewPath: to})
		default: // merkletrie.Modify
			out = append(out, nameStatusChange{OldPath: to, NewPath: to})
		}
	}
	return out, nil
}

// walkFiles visits every supported file under the worktree that falls inside
// the configured roots, with a repository-relative (slash-separated) path.
func walkFiles(dir string, roots []string, visit func(rel string) error) error {
	if len(roots) == 0 {
		roots = []string{""}
	}
	return filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if !inScope(rel, roots) || !isSupportedFile(rel) {
			return nil
		}
		return visit(rel)
	})
}

func readFile(dir, rel string) ([]byte, error) {
	return os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
}

func inScope(file string, roots []string) bool {
	if len(roots) == 0 {
		return true
	}
	for _, r := range roots {
		// An empty root means "whole repository" (the walkFiles sentinel for an
		// un-scoped sync).
		if r == "" || file == r || strings.HasPrefix(file, r+"/") {
			return true
		}
	}
	return false
}

// isSupportedFile limits repository sync to document formats the knowledge
// pipeline can ingest as text. Deliberately excludes loose image blobs: in a
// blog, images are meaningful only relative to their referencing markdown and
// are inlined by the connector (see image_inline.go), so standalone images
// would just become noise documents.
func isSupportedFile(file string) bool {
	_, ok := gitRepoSupportedFileExtensions[strings.ToLower(path.Ext(file))]
	return ok
}

var gitRepoSupportedFileExtensions = map[string]struct{}{
	".md": {}, ".markdown": {}, ".mdx": {},
	".html": {}, ".htm": {},
	".txt": {},
}

// isMarkdownish reports whether a file should get relative-image inlining.
func isMarkdownish(file string) bool {
	switch strings.ToLower(path.Ext(file)) {
	case ".md", ".markdown", ".mdx":
		return true
	default:
		return false
	}
}

// isHTMLish reports whether a file may carry relative <img src> references.
func isHTMLish(file string) bool {
	switch strings.ToLower(path.Ext(file)) {
	case ".html", ".htm":
		return true
	default:
		return false
	}
}
