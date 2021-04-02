package github

import (
	"context"
	"errors"
	"fmt"
	neturl "net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// A tokenRefresher can provide a fresh token for authenticating git operations
// with github
type tokenRefresher interface {
	refreshToken() (string, error)
}

// Repo manager gates access to all git operations. In this way, it is able to
// remove cloned repos after they are no longer needed in a thread-safe manner.
type repoManager struct {
	// Path to directory in which git repositories are cloned
	cloneDir string
	// Record of cloned repos under management
	managed map[string]*repo
	// TTL is the time after a repo is cloned before it is considered for
	// deletion
	ttl time.Duration

	mu sync.Mutex
}

func newRepoManager(cloneDir string) *repoManager {
	return &repoManager{
		cloneDir: cloneDir,
		managed:  make(map[string]*repo),
		// Repos are deleted at least one hour after they were last cloned
		ttl: time.Hour,
	}
}

// Clone clones a git repo to local disk and returns an obj representing it.
// Thereafter the caller has a limited time before the repo is deleted.
func (m *repoManager) clone(url, branch, sha, owner, name string, refresher tokenRefresher) (*repo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Check if repo is already cloned to disk
	repo, ok := m.managed[sha]
	if ok {
		// Reset TTL so that caller has time to use repo
		repo.lastCloned = time.Now()
		return repo, nil
	}

	// Clone repo and retain record of it
	repo, err := m.doClone(url, branch, sha, owner, name, refresher)
	if err != nil {
		return nil, err
	}
	m.managed[sha] = repo

	return repo, nil
}

func (m *repoManager) doClone(url, branch, sha, owner, name string, refresher tokenRefresher) (*repo, error) {
	// Clone repo to this path
	path := filepath.Join(m.cloneDir, sha)

	// First remove path if it already exists
	if err := os.RemoveAll(path); err != nil {
		return nil, fmt.Errorf("unable to remove git repo: %w", err)
	}

	// Create repo dir and any ancestor dirs
	if err := os.MkdirAll(path, 0700); err != nil {
		return nil, fmt.Errorf("unable to make directory for repo: %s: %w", path, err)
	}

	// Get access token for cloning repo
	token, err := refresher.refreshToken()
	if err != nil {
		return nil, err
	}

	src, err := neturl.Parse(url)
	if err != nil {
		return nil, fmt.Errorf("unable to parse repo URL: %w", err)
	}
	src.User = neturl.UserPassword("x-access-token", token)

	args := []string{"clone", "--branch", branch, "--depth=1", "--single-branch", src.String(), "."}
	if _, err := runGitCmd(path, args...); err != nil {
		// Redact token before propagating error
		return nil, errors.New(strings.ReplaceAll(err.Error(), src.String(), src.Redacted()))
	}

	return &repo{
		url:        url,
		branch:     branch,
		sha:        sha,
		owner:      owner,
		name:       name,
		path:       path,
		lastCloned: time.Now(),
	}, nil
}

// Run garbage collector that deletes local clones that have exceeded their TTL.
// Checks local clones every interval.
func (m *repoManager) reaper(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			m.mu.Lock()
			for _, r := range m.managed {
				if r.lastCloned.Add(m.ttl).Before(time.Now()) {
					// TTL exceeded
					_ = os.RemoveAll(r.path)
					delete(m.managed, r.sha)
				}
			}
			m.mu.Unlock()
		case <-ctx.Done():
			return
		}
	}
}

func runGitCmd(path string, args ...string) (string, error) {
	cmd := exec.Command("git", args...) // nolint: gosec
	cmd.Dir = path

	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("unable to run git command (git %v): %w: %s", args, err, string(out))
	}
	return string(out), nil
}