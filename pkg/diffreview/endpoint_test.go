package diffreview

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveBaseMaterializesOldAndReadsDirtyWorkingTree(t *testing.T) {
	repo := initRepo(t)
	writeFile(t, repo, "paper.tex", "base\n")
	git(t, repo, "add", "paper.tex")
	git(t, repo, "commit", "-m", "base")

	writeFile(t, repo, "paper.tex", "dirty working tree\n")

	oldEndpoint, newEndpoint, err := Resolver{
		WorkDir:   repo,
		SessionID: "test session",
	}.ResolveBase(context.Background(), "HEAD", "paper.tex")
	require.NoError(t, err)

	assert.Equal(t, GitBlob, oldEndpoint.Kind)
	assert.Equal(t, "HEAD:paper.tex", oldEndpoint.Spec)
	assert.Equal(t, "HEAD:paper.tex", oldEndpoint.Label)
	assertSamePath(t, repo, oldEndpoint.RepoRoot)
	assert.Equal(t, "paper.tex", oldEndpoint.RelPath)
	assert.False(t, oldEndpoint.Editable)
	assert.True(t, oldEndpoint.Materialized)
	assert.Equal(t, []byte("base\n"), oldEndpoint.Source)
	assert.Contains(t, filepath.ToSlash(oldEndpoint.Path), "/.mrevdiff/test-session/")

	gotOld, err := os.ReadFile(oldEndpoint.Path)
	require.NoError(t, err)
	assert.Equal(t, []byte("base\n"), gotOld)
	info, err := os.Stat(oldEndpoint.Path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o444), info.Mode().Perm())
	sessionDir := filepath.Join(repo, ".mrevdiff", "test-session")
	sessionInfo, err := os.Stat(sessionDir)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o700), sessionInfo.Mode().Perm())

	assert.Equal(t, WorkingFile, newEndpoint.Kind)
	assert.Equal(t, "working tree", newEndpoint.Label)
	assert.Equal(t, "paper.tex", newEndpoint.Spec)
	assertSamePath(t, repo, newEndpoint.RepoRoot)
	assert.Equal(t, "paper.tex", newEndpoint.RelPath)
	assert.Equal(t, filepath.Join(repo, "paper.tex"), newEndpoint.Path)
	assert.True(t, newEndpoint.Editable)
	assert.False(t, newEndpoint.Materialized)
	assert.Equal(t, []byte("dirty working tree\n"), newEndpoint.Source)
}

func TestResolveBaseUsesRepoRelativePathFromSubdirectory(t *testing.T) {
	repo := initRepo(t)
	writeFile(t, repo, "sections/paper.tex", "base\n")
	git(t, repo, "add", "sections/paper.tex")
	git(t, repo, "commit", "-m", "base")
	writeFile(t, repo, "sections/paper.tex", "new\n")

	oldEndpoint, newEndpoint, err := Resolver{
		WorkDir:   filepath.Join(repo, "sections"),
		SessionID: "subdir",
	}.ResolveBase(context.Background(), "HEAD", "paper.tex")
	require.NoError(t, err)

	assert.Equal(t, "sections/paper.tex", oldEndpoint.RelPath)
	assert.Equal(t, "HEAD:sections/paper.tex", oldEndpoint.Spec)
	assert.Equal(t, []byte("base\n"), oldEndpoint.Source)
	assert.Equal(t, "sections/paper.tex", newEndpoint.RelPath)
	assert.Equal(t, []byte("new\n"), newEndpoint.Source)
}

func TestResolveExplicitGitBlob(t *testing.T) {
	repo := initRepo(t)
	writeFile(t, repo, "paper.tex", "committed\n")
	git(t, repo, "add", "paper.tex")
	git(t, repo, "commit", "-m", "base")

	endpoint, err := Resolver{
		WorkDir:   repo,
		SessionID: "explicit",
	}.ResolveEndpoint(context.Background(), "HEAD:paper.tex", true)
	require.NoError(t, err)

	assert.Equal(t, GitBlob, endpoint.Kind)
	assert.Equal(t, "HEAD:paper.tex", endpoint.Label)
	assert.Equal(t, "paper.tex", endpoint.RelPath)
	assert.False(t, endpoint.Editable)
	assert.True(t, endpoint.Materialized)
	assert.Equal(t, []byte("committed\n"), endpoint.Source)

	got, err := os.ReadFile(endpoint.Path)
	require.NoError(t, err)
	assert.Equal(t, []byte("committed\n"), got)
}

func TestResolveEndpointsMarksOnlyNewFilesystemEndpointEditable(t *testing.T) {
	dir := t.TempDir()
	oldPath := filepath.Join(dir, "old.tex")
	newPath := filepath.Join(dir, "new.tex")
	require.NoError(t, os.WriteFile(oldPath, []byte("old"), 0o644))
	require.NoError(t, os.WriteFile(newPath, []byte("new"), 0o644))

	oldEndpoint, newEndpoint, err := Resolver{WorkDir: dir}.ResolveEndpoints(context.Background(), "old.tex", "new.tex")
	require.NoError(t, err)

	assert.Equal(t, WorkingFile, oldEndpoint.Kind)
	assert.False(t, oldEndpoint.Editable)
	assert.True(t, oldEndpoint.Materialized)
	assert.Equal(t, []byte("old"), oldEndpoint.Source)
	assert.NotEqual(t, oldPath, oldEndpoint.Path)
	assert.Contains(t, filepath.ToSlash(oldEndpoint.Path), "/.mrevdiff/")
	gotOld, err := os.ReadFile(oldEndpoint.Path)
	require.NoError(t, err)
	assert.Equal(t, []byte("old"), gotOld)
	info, err := os.Stat(oldEndpoint.Path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o444), info.Mode().Perm())

	assert.Equal(t, WorkingFile, newEndpoint.Kind)
	assert.True(t, newEndpoint.Editable)
	assert.False(t, newEndpoint.Materialized)
	assert.Equal(t, newPath, newEndpoint.Path)
	assert.Equal(t, []byte("new"), newEndpoint.Source)
}

func TestMaterializeRejectsSymlinkedCacheDirectory(t *testing.T) {
	repo := initRepo(t)
	writeFile(t, repo, "paper.tex", "committed\n")
	git(t, repo, "add", "paper.tex")
	git(t, repo, "commit", "-m", "base")
	outside := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(repo, ".mrevdiff", "safe-session"), 0o700))
	require.NoError(t, os.Symlink(outside, filepath.Join(repo, ".mrevdiff", "safe-session", "HEAD")))

	endpoint, err := Resolver{
		WorkDir:   repo,
		SessionID: "safe-session",
	}.ResolveEndpoint(context.Background(), "HEAD:paper.tex", false)
	require.Error(t, err)
	assert.Empty(t, endpoint.Path)
	_, outsideErr := os.Stat(filepath.Join(outside, "paper.tex"))
	assert.True(t, os.IsNotExist(outsideErr), "materialization wrote through symlink")
}

func TestResolveEndpointRejectsMissingFilesystemSpec(t *testing.T) {
	dir := t.TempDir()
	_, err := Resolver{WorkDir: dir}.ResolveEndpoint(context.Background(), "missing.tex", false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "file does not exist")
}

func TestResolveEndpointRejectsUnsafeGitBlobPaths(t *testing.T) {
	repo := initRepo(t)
	writeFile(t, repo, "paper.tex", "committed\n")
	git(t, repo, "add", "paper.tex")
	git(t, repo, "commit", "-m", "base")

	for _, spec := range []string{"HEAD:../secret.tex", "HEAD:/tmp/file.tex"} {
		endpoint, err := Resolver{WorkDir: repo, SessionID: "../unsafe session"}.ResolveEndpoint(context.Background(), spec, false)
		require.Error(t, err, spec)
		assert.Empty(t, endpoint.Path)
	}
}

func TestResolveEndpointRejectsOptionLikeGitRevision(t *testing.T) {
	repo := initRepo(t)
	writeFile(t, repo, "paper.tex", "committed\n")
	git(t, repo, "add", "paper.tex")
	git(t, repo, "commit", "-m", "base")

	endpoint, err := Resolver{WorkDir: repo}.ResolveEndpoint(context.Background(), "--output=/tmp/poc:paper.tex", false)
	require.Error(t, err)
	assert.Empty(t, endpoint.Path)
	assert.Contains(t, err.Error(), "git revision must not start")
}

func initRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	git(t, dir, "init")
	git(t, dir, "config", "user.name", "Test User")
	git(t, dir, "config", "user.email", "test@example.com")
	git(t, dir, "config", "commit.gpgsign", "false")
	return dir
}

func assertSamePath(t *testing.T, expected, actual string) {
	t.Helper()
	if expected == actual {
		return
	}
	exp, expErr := filepath.EvalSymlinks(expected)
	act, actErr := filepath.EvalSymlinks(actual)
	if expErr == nil && actErr == nil && exp == act {
		return
	}
	assert.Equal(t, expected, actual)
}

func writeFile(t *testing.T, repo, relPath, content string) {
	t.Helper()
	path := filepath.Join(repo, filepath.FromSlash(relPath))
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
}

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}
