package ui

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// lmkfTestEnv redirects the wire-protocol directories to a temp root and
// shrinks the poll/settle timings so the wait tests run fast.
func lmkfTestEnv(t *testing.T) (statusDir, locksDir string) {
	t.Helper()
	root := t.TempDir()
	statusDir = filepath.Join(root, "lmkf-status")
	locksDir = filepath.Join(root, "lmk-locks")
	if err := os.MkdirAll(statusDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(locksDir, 0o755); err != nil {
		t.Fatal(err)
	}
	origStatus, origLocks := lmkfStatusDir, lmkLocksDir
	origPoll, origSettle := lmkfPollInterval, lmkfSettleWindow
	lmkfStatusDir, lmkLocksDir = statusDir, locksDir
	lmkfPollInterval = 10 * time.Millisecond
	lmkfSettleWindow = 150 * time.Millisecond
	t.Cleanup(func() {
		lmkfStatusDir, lmkLocksDir = origStatus, origLocks
		lmkfPollInterval, lmkfSettleWindow = origPoll, origSettle
	})
	return statusDir, locksDir
}

func writeLmkLock(t *testing.T, texPath string, pid int, cmd string) {
	t.Helper()
	lock := lmkLockPath(canonicalPath(texPath))
	if err := os.MkdirAll(lock, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(lock, "pid"), []byte(fmt.Sprintf("%d\n", pid)), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(lock, "cmd"), []byte(cmd+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// deadPID returns a pid that belonged to a real process which has since
// exited, so kill(pid, 0) fails.
func deadPID(t *testing.T) int {
	t.Helper()
	cmd := exec.Command("true")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	pid := cmd.Process.Pid
	if err := cmd.Wait(); err != nil {
		t.Fatal(err)
	}
	return pid
}

func lmkfProject(t *testing.T, statusDir string) (texPath, logPath string) {
	t.Helper()
	dir := t.TempDir()
	texPath = filepath.Join(dir, "paper.tex")
	logPath = filepath.Join(dir, "paper.log")
	if err := os.WriteFile(texPath, []byte("\\documentclass{article}"), 0o644); err != nil {
		t.Fatal(err)
	}
	statusFile := filepath.Join(statusDir, filepath.Base(dir))
	if err := os.WriteFile(statusFile, []byte(logPath+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return texPath, logPath
}

func TestLmkfWatchingRequiresLiveLock(t *testing.T) {
	statusDir, _ := lmkfTestEnv(t)
	texPath, _ := lmkfProject(t, statusDir)

	if LmkfWatching(texPath) {
		t.Fatalf("status file without any lock must not count as watching")
	}

	writeLmkLock(t, texPath, deadPID(t), "lmkf")
	if LmkfWatching(texPath) {
		t.Fatalf("status file with a dead lock pid must not count as watching")
	}

	writeLmkLock(t, texPath, os.Getpid(), "lmkf")
	if !LmkfWatching(texPath) {
		t.Fatalf("status file + live lock should count as watching")
	}
}

func TestLmkfWatchForAdoptsProjectMainFile(t *testing.T) {
	statusDir, _ := lmkfTestEnv(t)
	texPath, _ := lmkfProject(t, statusDir)
	writeLmkLock(t, texPath, os.Getpid(), "lmkf")

	subfile := filepath.Join(filepath.Dir(texPath), "section2.tex")
	if err := os.WriteFile(subfile, []byte("\\section{Two}"), 0o644); err != nil {
		t.Fatal(err)
	}

	watch, ok := LmkfWatchFor(subfile)
	if !ok {
		t.Fatalf("project watch should be visible from a sibling subfile")
	}
	if watch.MainTex != texPath {
		t.Fatalf("MainTex = %q, want %q", watch.MainTex, texPath)
	}
	if LmkfWatching(subfile) {
		t.Fatalf("subfile is not the watched main file")
	}
	if !LmkfWatching(texPath) {
		t.Fatalf("main file should report watching")
	}
}

func TestLmkfWatchForRejectsForeignDirectory(t *testing.T) {
	statusDir, _ := lmkfTestEnv(t)
	texPath, _ := lmkfProject(t, statusDir)
	writeLmkLock(t, texPath, os.Getpid(), "lmkf")

	// A same-basename project directory elsewhere collides on the status
	// key but its log lives in the other directory.
	otherRoot := t.TempDir()
	otherDir := filepath.Join(otherRoot, filepath.Base(filepath.Dir(texPath)))
	if err := os.MkdirAll(otherDir, 0o755); err != nil {
		t.Fatal(err)
	}
	otherTex := filepath.Join(otherDir, "paper.tex")
	if err := os.WriteFile(otherTex, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok := LmkfWatchFor(otherTex); ok {
		t.Fatalf("status file pointing into a different directory must be ignored")
	}
}

const completedLogOK = "some latexmk output\nHere is how much of TeX's memory you used\nOutput written on paper.pdf (3 pages)\n"
const runningLog = "This is pdfTeX\n(./paper.tex\n"

// TestWaitObservedPassAcceptedImmediately pins the common case: the log is
// stale at reload time, our pass truncates it (marker disappears), then the
// marker reappears — accepted with no settle delay.
func TestWaitObservedPassAcceptedImmediately(t *testing.T) {
	lmkfTestEnv(t)
	dir := t.TempDir()
	logPath := filepath.Join(dir, "paper.log")
	old := time.Now().Add(-time.Hour)
	if err := os.WriteFile(logPath, []byte(completedLogOK), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(logPath, old, old); err != nil {
		t.Fatal(err)
	}
	editTime := time.Now()

	go func() {
		time.Sleep(40 * time.Millisecond)
		_ = os.WriteFile(logPath, []byte(runningLog), 0o644) // pass starts
		time.Sleep(40 * time.Millisecond)
		_ = os.WriteFile(logPath, []byte(completedLogOK), 0o644) // pass ends
	}()

	start := time.Now()
	result, errLine := waitForLmkfComplete(logPath, editTime, 3*time.Second)
	if result != "ok" || errLine != "" {
		t.Fatalf("result = %q %q, want ok", result, errLine)
	}
	if elapsed := time.Since(start); elapsed >= lmkfSettleWindow+80*time.Millisecond {
		t.Fatalf("observed pass should be accepted without settle delay, took %v", elapsed)
	}
}

// TestWaitSkipsPreEditPass pins the race fix: a pass already in flight at
// edit time completes first (pre-edit content) and must NOT be accepted,
// because lmkf immediately starts the real pass.
func TestWaitSkipsPreEditPass(t *testing.T) {
	lmkfTestEnv(t)
	dir := t.TempDir()
	logPath := filepath.Join(dir, "paper.log")
	// In-flight pass at edit time: marker absent.
	if err := os.WriteFile(logPath, []byte(runningLog), 0o644); err != nil {
		t.Fatal(err)
	}
	editTime := time.Now()

	preEditDone := make(chan time.Time, 1)
	go func() {
		time.Sleep(30 * time.Millisecond)
		_ = os.WriteFile(logPath, []byte(completedLogOK+"PREEDIT\n"), 0o644) // pre-edit pass completes
		preEditDone <- time.Now()
		time.Sleep(60 * time.Millisecond) // < settle window: latexmk notices the edit
		_ = os.WriteFile(logPath, []byte(runningLog), 0o644) // real pass starts
		time.Sleep(40 * time.Millisecond)
		_ = os.WriteFile(logPath, []byte(completedLogOK+"POSTEDIT\n"), 0o644)
	}()

	result, errLine := waitForLmkfComplete(logPath, editTime, 3*time.Second)
	if result != "ok" || errLine != "" {
		t.Fatalf("result = %q %q, want ok", result, errLine)
	}
	done := <-preEditDone
	if time.Now().Before(done.Add(60 * time.Millisecond)) {
		t.Fatalf("returned before the post-edit pass could have completed")
	}
	data, _ := os.ReadFile(logPath)
	if string(data) != completedLogOK+"POSTEDIT\n" {
		t.Fatalf("accepted log state is not the post-edit pass: %q", data)
	}
}

// TestWaitAmbiguousPassAcceptedAfterSettle pins the no-second-pass case: a
// completed pass of unknown start time is accepted once the settle window
// proves latexmk saw nothing newer to build.
func TestWaitAmbiguousPassAcceptedAfterSettle(t *testing.T) {
	lmkfTestEnv(t)
	dir := t.TempDir()
	logPath := filepath.Join(dir, "paper.log")
	editTime := time.Now().Add(-10 * time.Millisecond)
	if err := os.WriteFile(logPath, []byte(completedLogOK), 0o644); err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	result, _ := waitForLmkfComplete(logPath, editTime, 3*time.Second)
	if result != "ok" {
		t.Fatalf("result = %q, want ok", result)
	}
	if elapsed := time.Since(start); elapsed < lmkfSettleWindow {
		t.Fatalf("ambiguous pass accepted before settle window elapsed (%v)", elapsed)
	}
}

func TestWaitReportsLogErrors(t *testing.T) {
	lmkfTestEnv(t)
	dir := t.TempDir()
	logPath := filepath.Join(dir, "paper.log")
	body := "! Undefined control sequence.\n" + completedLogOK
	if err := os.WriteFile(logPath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	result, errLine := waitForLmkfComplete(logPath, time.Now().Add(-time.Second), 2*time.Second)
	if result != "error" {
		t.Fatalf("result = %q, want error", result)
	}
	if errLine == "" {
		t.Fatalf("expected the first error line to be surfaced")
	}
}

func TestWaitTimesOutWithoutAnyPass(t *testing.T) {
	lmkfTestEnv(t)
	dir := t.TempDir()
	logPath := filepath.Join(dir, "paper.log")
	if err := os.WriteFile(logPath, []byte(runningLog), 0o644); err != nil {
		t.Fatal(err)
	}
	result, _ := waitForLmkfComplete(logPath, time.Now(), 100*time.Millisecond)
	if result != "timeout" {
		t.Fatalf("result = %q, want timeout", result)
	}
}

func TestAcquireLmkBuildLock(t *testing.T) {
	_, _ = lmkfTestEnv(t)
	dir := t.TempDir()
	texPath := filepath.Join(dir, "paper.tex")
	if err := os.WriteFile(texPath, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	release, holder, ok := AcquireLmkBuildLock(texPath)
	if !ok {
		t.Fatalf("fresh lock should be acquired, holder=%q", holder)
	}
	if _, _, ok2 := AcquireLmkBuildLock(texPath); ok2 {
		t.Fatalf("second acquire while held by a live pid must fail")
	}
	release()

	// A lock left by a dead process is taken over.
	writeLmkLock(t, texPath, deadPID(t), "lmk")
	release, _, ok = AcquireLmkBuildLock(texPath)
	if !ok {
		t.Fatalf("dead lock should be taken over")
	}
	release()
}
