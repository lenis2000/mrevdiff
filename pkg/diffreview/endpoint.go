package diffreview

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"unicode"
)

// EndpointKind describes where an endpoint's source came from.
type EndpointKind int

const (
	WorkingFile EndpointKind = iota
	GitBlob
)

func (k EndpointKind) String() string {
	switch k {
	case WorkingFile:
		return "working-file"
	case GitBlob:
		return "git-blob"
	default:
		return fmt.Sprintf("EndpointKind(%d)", int(k))
	}
}

// Endpoint is one side of a diff review.
type Endpoint struct {
	Kind         EndpointKind
	Label        string // e.g. "master:paper.tex" or "working tree"
	Spec         string
	RepoRoot     string
	RelPath      string
	Path         string // real path for working file; materialized path for git blob
	Editable     bool
	Source       []byte
	Materialized bool
}

// Resolver resolves working-tree and git-object endpoints for diff review.
type Resolver struct {
	// WorkDir is the directory used to resolve relative paths and run git
	// plumbing commands. If empty, the current process directory is used.
	WorkDir string
	// SessionID names the stable .mrevdiff subdirectory used for
	// materialized git blobs. If empty, a timestamp-based ID is generated.
	SessionID string
}

// ResolveBase resolves the primary form:
//
//	mrevdiff --base REV path.tex
//
// The old endpoint is REV:<repo-relative path>; the new endpoint is the
// working-tree file.
func (r Resolver) ResolveBase(ctx context.Context, baseRev, path string) (Endpoint, Endpoint, error) {
	baseRev = strings.TrimSpace(baseRev)
	if strings.TrimSpace(baseRev) == "" {
		return Endpoint{}, Endpoint{}, errors.New("base revision is required")
	}
	if strings.TrimSpace(path) == "" {
		return Endpoint{}, Endpoint{}, errors.New("path is required")
	}

	workDir, err := r.workDir()
	if err != nil {
		return Endpoint{}, Endpoint{}, err
	}
	absPath, err := absFrom(workDir, path)
	if err != nil {
		return Endpoint{}, Endpoint{}, err
	}

	newEndpoint, err := r.resolveWorkingFile(ctx, path, absPath, true)
	if err != nil {
		return Endpoint{}, Endpoint{}, err
	}
	if newEndpoint.RepoRoot == "" {
		return Endpoint{}, Endpoint{}, fmt.Errorf("cannot resolve --base for %q: not inside a git repository", path)
	}

	oldSpec := baseRev + ":" + newEndpoint.RelPath
	oldEndpoint, err := r.resolveGitBlobInRepo(ctx, newEndpoint.RepoRoot, oldSpec, baseRev, newEndpoint.RelPath)
	if err != nil {
		return Endpoint{}, Endpoint{}, err
	}
	return oldEndpoint, newEndpoint, nil
}

// ResolveEndpoints resolves the explicit form:
//
//	mrevdiff OLD NEW
//
// Filesystem endpoints are read from disk. Git endpoint specs use REV:path.
// Only a filesystem NEW endpoint is editable.
func (r Resolver) ResolveEndpoints(ctx context.Context, oldSpec, newSpec string) (Endpoint, Endpoint, error) {
	oldEndpoint, err := r.ResolveEndpoint(ctx, oldSpec, false)
	if err != nil {
		return Endpoint{}, Endpoint{}, fmt.Errorf("resolve old endpoint: %w", err)
	}
	if oldEndpoint.Kind == WorkingFile {
		oldEndpoint, err = r.materializeWorkingSnapshot(oldEndpoint)
		if err != nil {
			return Endpoint{}, Endpoint{}, fmt.Errorf("materialize old endpoint: %w", err)
		}
	}
	newEndpoint, err := r.ResolveEndpoint(ctx, newSpec, true)
	if err != nil {
		return Endpoint{}, Endpoint{}, fmt.Errorf("resolve new endpoint: %w", err)
	}
	return oldEndpoint, newEndpoint, nil
}

// ResolveEndpoint resolves one endpoint. editable is honored only for
// filesystem endpoints; git blobs are always read-only snapshots.
func (r Resolver) ResolveEndpoint(ctx context.Context, spec string, editable bool) (Endpoint, error) {
	if strings.TrimSpace(spec) == "" {
		return Endpoint{}, errors.New("endpoint spec is required")
	}
	workDir, err := r.workDir()
	if err != nil {
		return Endpoint{}, err
	}
	absPath, err := absFrom(workDir, spec)
	if err != nil {
		return Endpoint{}, err
	}
	if _, statErr := os.Stat(absPath); statErr == nil {
		return r.resolveWorkingFile(ctx, spec, absPath, editable)
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return Endpoint{}, fmt.Errorf("stat %q: %w", spec, statErr)
	}
	if rev, relPath, ok := splitGitEndpoint(spec); ok {
		return r.resolveGitBlob(ctx, spec, rev, relPath)
	}
	return Endpoint{}, fmt.Errorf("cannot read %q: file does not exist", spec)
}

// ResolveBase resolves a --base pair using a one-off Resolver.
func ResolveBase(ctx context.Context, workDir, baseRev, path, sessionID string) (Endpoint, Endpoint, error) {
	return Resolver{WorkDir: workDir, SessionID: sessionID}.ResolveBase(ctx, baseRev, path)
}

// ResolveEndpoints resolves an explicit OLD NEW pair using a one-off Resolver.
func ResolveEndpoints(ctx context.Context, workDir, oldSpec, newSpec, sessionID string) (Endpoint, Endpoint, error) {
	return Resolver{WorkDir: workDir, SessionID: sessionID}.ResolveEndpoints(ctx, oldSpec, newSpec)
}

func (r Resolver) resolveWorkingFile(ctx context.Context, spec, absPath string, editable bool) (Endpoint, error) {
	source, err := os.ReadFile(absPath)
	if err != nil {
		return Endpoint{}, fmt.Errorf("cannot read %q: %w", spec, err)
	}
	repoRoot, relPath := repoInfoForPath(ctx, filepath.Dir(absPath), absPath)
	return Endpoint{
		Kind:     WorkingFile,
		Label:    "working tree",
		Spec:     spec,
		RepoRoot: repoRoot,
		RelPath:  relPath,
		Path:     absPath,
		Editable: editable,
		Source:   source,
	}, nil
}

func (r Resolver) resolveGitBlob(ctx context.Context, spec, rev, relPath string) (Endpoint, error) {
	workDir, err := r.workDir()
	if err != nil {
		return Endpoint{}, err
	}
	repoRoot, err := gitRoot(ctx, workDir)
	if err != nil {
		return Endpoint{}, fmt.Errorf("git root for %q: %w", spec, err)
	}
	return r.resolveGitBlobInRepo(ctx, repoRoot, spec, rev, relPath)
}

func (r Resolver) resolveGitBlobInRepo(ctx context.Context, repoRoot, spec, rev, relPath string) (Endpoint, error) {
	rev = strings.TrimSpace(rev)
	if rev == "" {
		return Endpoint{}, errors.New("git revision is required")
	}
	if strings.HasPrefix(rev, "-") {
		return Endpoint{}, fmt.Errorf("git revision must not start with '-': %q", rev)
	}
	relPath = filepath.ToSlash(filepath.Clean(relPath))
	if strings.HasPrefix(relPath, "../") || relPath == ".." || filepath.IsAbs(relPath) {
		return Endpoint{}, fmt.Errorf("git blob path must be repository-relative: %q", relPath)
	}
	source, err := gitShow(ctx, repoRoot, rev, relPath)
	if err != nil {
		return Endpoint{}, err
	}
	sessionID := r.sessionID()
	matPath, err := materializePath(repoRoot, sessionID, rev, relPath)
	if err != nil {
		return Endpoint{}, err
	}
	if err := writeMaterializedFile(repoRoot, matPath, source); err != nil {
		return Endpoint{}, fmt.Errorf("materialize %q: %w", spec, err)
	}

	return Endpoint{
		Kind:         GitBlob,
		Label:        spec,
		Spec:         spec,
		RepoRoot:     repoRoot,
		RelPath:      relPath,
		Path:         matPath,
		Editable:     false,
		Source:       source,
		Materialized: true,
	}, nil
}

func (r Resolver) materializeWorkingSnapshot(endpoint Endpoint) (Endpoint, error) {
	root := endpoint.RepoRoot
	if root == "" {
		var err error
		root, err = r.workDir()
		if err != nil {
			return Endpoint{}, err
		}
	}
	relPath := endpoint.RelPath
	if relPath == "" {
		relPath = filepath.Base(endpoint.Path)
	}
	matPath, err := materializePath(root, r.sessionID(), "old-file", relPath)
	if err != nil {
		return Endpoint{}, err
	}
	if err := writeMaterializedFile(root, matPath, endpoint.Source); err != nil {
		return Endpoint{}, fmt.Errorf("materialize %q: %w", endpoint.Spec, err)
	}
	endpoint.Path = matPath
	endpoint.Editable = false
	endpoint.Materialized = true
	return endpoint, nil
}

func (r Resolver) workDir() (string, error) {
	if r.WorkDir == "" {
		return os.Getwd()
	}
	abs, err := filepath.Abs(r.WorkDir)
	if err != nil {
		return "", fmt.Errorf("resolve work dir %q: %w", r.WorkDir, err)
	}
	return abs, nil
}

func (r Resolver) sessionID() string {
	if strings.TrimSpace(r.SessionID) != "" {
		return safePathComponent(r.SessionID)
	}
	return "session-" + time.Now().UTC().Format("20060102T150405.000000000Z")
}

func absFrom(workDir, path string) (string, error) {
	if filepath.IsAbs(path) {
		return filepath.Clean(path), nil
	}
	return filepath.Abs(filepath.Join(workDir, path))
}

func repoInfoForPath(ctx context.Context, dir, absPath string) (repoRoot, relPath string) {
	root, err := gitRoot(ctx, dir)
	if err != nil {
		return "", filepath.Base(absPath)
	}
	relRoot := root
	relPathBase := absPath
	// On macOS, git may report /private/var/... while t.TempDir (and user
	// paths) use /var/.... Canonicalize only for the Rel calculation so
	// --base from a symlinked temp/root still gets a repository-relative path.
	if realRoot, err := filepath.EvalSymlinks(root); err == nil {
		relRoot = realRoot
	}
	if realAbs, err := filepath.EvalSymlinks(absPath); err == nil {
		relPathBase = realAbs
	}
	rel, err := filepath.Rel(relRoot, relPathBase)
	if err != nil || rel == "." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) || rel == ".." {
		return root, filepath.Base(absPath)
	}
	return root, filepath.ToSlash(rel)
}

func gitRoot(ctx context.Context, dir string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", "rev-parse", "--show-toplevel")
	cmd.Dir = dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg != "" {
			return "", fmt.Errorf("%w: %s", err, msg)
		}
		return "", err
	}
	return filepath.Clean(strings.TrimSpace(string(out))), nil
}

func gitShow(ctx context.Context, repoRoot, rev, relPath string) ([]byte, error) {
	spec := rev + ":" + filepath.ToSlash(relPath)
	cmd := exec.CommandContext(ctx, "git", "show", "--end-of-options", spec)
	cmd.Dir = repoRoot
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg != "" {
			return nil, fmt.Errorf("git show %s: %w: %s", spec, err, msg)
		}
		return nil, fmt.Errorf("git show %s: %w", spec, err)
	}
	return out, nil
}

func splitGitEndpoint(spec string) (rev, relPath string, ok bool) {
	idx := strings.Index(spec, ":")
	if idx <= 0 || idx == len(spec)-1 {
		return "", "", false
	}
	rev = strings.TrimSpace(spec[:idx])
	relPath = strings.TrimSpace(spec[idx+1:])
	if rev == "" || relPath == "" || filepath.IsAbs(relPath) {
		return "", "", false
	}
	return rev, filepath.ToSlash(filepath.Clean(relPath)), true
}

func materializePath(repoRoot, sessionID, rev, relPath string) (string, error) {
	if repoRoot == "" {
		return "", errors.New("repo root is required")
	}
	sessionID = safePathComponent(sessionID)
	if sessionID == "" {
		return "", errors.New("session ID is required")
	}
	revComponent := safePathComponent(rev)
	if revComponent == "" {
		revComponent = "rev-" + sha8(rev)
	}
	cleanRel := filepath.Clean(filepath.FromSlash(relPath))
	if cleanRel == "." || strings.HasPrefix(cleanRel, ".."+string(os.PathSeparator)) || cleanRel == ".." || filepath.IsAbs(cleanRel) {
		return "", fmt.Errorf("materialized path must be repository-relative: %q", relPath)
	}
	return filepath.Join(repoRoot, ".mrevdiff", sessionID, revComponent, cleanRel), nil
}

func writeMaterializedFile(root, target string, data []byte) error {
	root = filepath.Clean(root)
	baseDir := filepath.Join(root, ".mrevdiff")
	rel, err := filepath.Rel(baseDir, target)
	if err != nil || rel == "." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) || rel == ".." || filepath.IsAbs(rel) {
		return fmt.Errorf("materialized target escapes .mrevdiff: %q", target)
	}
	dir := filepath.Dir(target)
	if err := ensureDirNoSymlinks(root, dir, 0o700); err != nil {
		return err
	}
	if info, err := os.Lstat(target); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("materialized target is a symlink: %q", target)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("materialized target is not a regular file: %q", target)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}

	tmp, err := os.CreateTemp(dir, ".mrevdiff-materialized-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpName)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	_ = os.Chmod(tmpName, 0o444)
	if err := os.Rename(tmpName, target); err != nil {
		return err
	}
	cleanup = false
	return nil
}

func ensureDirNoSymlinks(root, dir string, mode os.FileMode) error {
	root = filepath.Clean(root)
	dir = filepath.Clean(dir)
	rel, err := filepath.Rel(root, dir)
	if err != nil || rel == "." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) || rel == ".." || filepath.IsAbs(rel) {
		return fmt.Errorf("materialization directory escapes root: %q", dir)
	}
	cur := root
	if rel == "." {
		return nil
	}
	for _, part := range strings.Split(rel, string(os.PathSeparator)) {
		if part == "" || part == "." {
			continue
		}
		cur = filepath.Join(cur, part)
		info, err := os.Lstat(cur)
		if errors.Is(err, os.ErrNotExist) {
			if err := os.Mkdir(cur, mode); err != nil && !errors.Is(err, os.ErrExist) {
				return err
			}
			info, err = os.Lstat(cur)
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("materialization directory is a symlink: %q", cur)
		}
		if !info.IsDir() {
			return fmt.Errorf("materialization path is not a directory: %q", cur)
		}
		_ = os.Chmod(cur, mode)
	}
	return nil
}

func safePathComponent(s string) string {
	s = strings.TrimSpace(s)
	var b strings.Builder
	lastDash := false
	for _, r := range s {
		switch {
		case r == '.' || r == '_' || r == '-':
			b.WriteRune(r)
			lastDash = false
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
			lastDash = false
		default:
			if !lastDash && b.Len() > 0 {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	out := strings.Trim(b.String(), ".-")
	if len(out) > 64 {
		out = out[:64]
		out = strings.TrimRight(out, ".-")
	}
	return out
}

func sha8(s string) string {
	sum := sha1.Sum([]byte(s))
	return hex.EncodeToString(sum[:])[:8]
}
