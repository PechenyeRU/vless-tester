package output_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/whitedns/vless-tester/internal/output"
)

// initBareRepo creates an empty bare git repository and returns its file:// URL.
func initBareRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	bare := filepath.Join(t.TempDir(), "remote.git")
	if out, err := exec.Command("git", "init", "--bare", "-b", "main", bare).CombinedOutput(); err != nil {
		t.Fatalf("init bare: %v: %s", err, out)
	}
	return "file://" + bare
}

func TestGitPublisherPushesArtifacts(t *testing.T) {
	repoURL := initBareRepo(t)
	work := filepath.Join(t.TempDir(), "work")

	pub := &output.GitPublisher{RepoURL: repoURL, Branch: "main", WorkDir: work}
	files := map[string][]byte{
		output.FileSubscription: []byte("c3Vic2NyaXB0aW9u"),
		output.FileReadme:       []byte("# list\n"),
	}
	if err := pub.Publish(context.Background(), files, "publish: update list"); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	// Clone the remote afresh and verify the files landed.
	checkout := filepath.Join(t.TempDir(), "verify")
	if out, err := exec.Command("git", "clone", repoURL, checkout).CombinedOutput(); err != nil {
		t.Fatalf("verify clone: %v: %s", err, out)
	}
	got, err := os.ReadFile(filepath.Join(checkout, output.FileReadme))
	if err != nil {
		t.Fatalf("read pushed README: %v", err)
	}
	if string(got) != "# list\n" {
		t.Fatalf("pushed README = %q", got)
	}
}

func TestGitPublisherIdempotent(t *testing.T) {
	repoURL := initBareRepo(t)
	work := filepath.Join(t.TempDir(), "work")
	pub := &output.GitPublisher{RepoURL: repoURL, Branch: "main", WorkDir: work}
	files := map[string][]byte{output.FileReadme: []byte("# same\n")}

	if err := pub.Publish(context.Background(), files, "first"); err != nil {
		t.Fatalf("first publish: %v", err)
	}
	// Publishing identical content again must be a no-op, not an error.
	if err := pub.Publish(context.Background(), files, "second"); err != nil {
		t.Fatalf("second publish should be a no-op: %v", err)
	}

	countOut, err := exec.Command("git", "-C", work, "rev-list", "--count", "HEAD").CombinedOutput()
	if err != nil {
		t.Fatalf("rev-list: %v: %s", err, countOut)
	}
	if got := string(countOut); got != "1\n" {
		t.Fatalf("commit count = %q, want 1 (no empty second commit)", got)
	}
}

func TestMockPublisher(t *testing.T) {
	var m output.MockPublisher
	files := map[string][]byte{"a.txt": []byte("x")}
	if err := m.Publish(context.Background(), files, "msg"); err != nil {
		t.Fatalf("mock publish: %v", err)
	}
	if m.Calls != 1 || m.Message != "msg" || string(m.Files["a.txt"]) != "x" {
		t.Fatalf("mock did not record call: %+v", m)
	}
}
