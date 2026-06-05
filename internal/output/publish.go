package output

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
)

// Publisher writes a set of artifact files somewhere durable. The interface is
// mockable so the engine can be tested without touching a real repository.
type Publisher interface {
	Publish(ctx context.Context, files map[string][]byte, message string) error
}

// GitPublisher commits artifacts to a dedicated Git repository and pushes them.
// The repository is intentionally separate from the source tree (OSINT: only
// the public list is exposed).
type GitPublisher struct {
	RepoURL    string // clone URL (https with token, ssh, or file:// for tests)
	Branch     string // defaults to "main"
	WorkDir    string // local checkout path; a temp dir is used when empty
	AuthorName string
	AuthorMail string
}

// Publish clones (or reuses) the repo, writes the files, commits and pushes.
func (g *GitPublisher) Publish(ctx context.Context, files map[string][]byte, message string) error {
	branch := g.Branch
	if branch == "" {
		branch = "main"
	}
	dir := g.WorkDir
	if dir == "" {
		tmp, err := os.MkdirTemp("", "vless-publish-*")
		if err != nil {
			return fmt.Errorf("publish: temp dir: %w", err)
		}
		defer os.RemoveAll(tmp)
		dir = tmp
	}

	if err := g.ensureCheckout(ctx, dir, branch); err != nil {
		return err
	}
	if err := writeFiles(dir, files); err != nil {
		return err
	}

	if err := g.git(ctx, dir, "add", "-A"); err != nil {
		return err
	}
	// Nothing staged means nothing changed; treat as success.
	if g.nothingToCommit(ctx, dir) {
		return nil
	}
	if err := g.commit(ctx, dir, message); err != nil {
		return err
	}
	return g.git(ctx, dir, "push", "origin", branch)
}

func (g *GitPublisher) ensureCheckout(ctx context.Context, dir, branch string) error {
	if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
		return g.git(ctx, dir, "pull", "--ff-only", "origin", branch)
	}
	if err := runGit(ctx, "", "clone", "--branch", branch, "--single-branch", g.RepoURL, dir); err != nil {
		// The branch may not exist yet on an empty repo; clone then create it.
		if err := runGit(ctx, "", "clone", g.RepoURL, dir); err != nil {
			return fmt.Errorf("publish: clone: %w", err)
		}
		if err := g.git(ctx, dir, "checkout", "-B", branch); err != nil {
			return err
		}
	}
	return nil
}

func (g *GitPublisher) commit(ctx context.Context, dir, message string) error {
	name := g.AuthorName
	if name == "" {
		name = "vless-tester"
	}
	mail := g.AuthorMail
	if mail == "" {
		mail = "bot@localhost"
	}
	return runGit(ctx, dir, "-c", "user.name="+name, "-c", "user.email="+mail, "commit", "-m", message)
}

func (g *GitPublisher) nothingToCommit(ctx context.Context, dir string) bool {
	out, err := exec.CommandContext(ctx, "git", "-C", dir, "status", "--porcelain").Output()
	return err == nil && len(out) == 0
}

func (g *GitPublisher) git(ctx context.Context, dir string, args ...string) error {
	return runGit(ctx, dir, args...)
}

func runGit(ctx context.Context, dir string, args ...string) error {
	if dir != "" {
		args = append([]string{"-C", dir}, args...)
	}
	cmd := exec.CommandContext(ctx, "git", args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git %v: %w: %s", args, err, out)
	}
	return nil
}

// writeFiles writes each artifact into dir, in a stable order.
func writeFiles(dir string, files map[string][]byte) error {
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if err := os.WriteFile(filepath.Join(dir, name), files[name], 0o644); err != nil {
			return fmt.Errorf("publish: write %s: %w", name, err)
		}
	}
	return nil
}

// MockPublisher records the last published artifacts for tests.
type MockPublisher struct {
	Files   map[string][]byte
	Message string
	Calls   int
}

// Publish stores the files in memory.
func (m *MockPublisher) Publish(_ context.Context, files map[string][]byte, message string) error {
	m.Files = files
	m.Message = message
	m.Calls++
	return nil
}
