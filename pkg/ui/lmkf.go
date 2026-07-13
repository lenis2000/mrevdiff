// lmkf integration: LP's lmkf shell wrapper runs `latexmk -pvc` on the
// side and owns the build pipeline. The wire protocol below is lmkf's
// external contract and must not be renamed: a status file at
// /tmp/lmkf-status/<basename-of-tex-dir> holds the absolute path of the
// .log file the watched latexmk is producing, and every completed
// pdflatex pass prints the marker "Here is how much of TeX" at the end
// of the log.
//
// A second contract comes from ~/.vim/lmk-guard.sh, shared by lmk, lmkf
// and vim's \lx compile: whoever runs latexmk on a .tex file holds a lock
// directory at /tmp/lmk-locks/<md5-of-abs-tex-path>.lock containing a
// `pid` file (plus `file`/`cmd`/`session`). A status file whose lock pid
// is dead is a leftover from a killed lmkf and must not be trusted — and
// mrevdiff must hold the same lock while running its own latexmk so it
// never races lmk/lmkf/\lx on the build directory.
package ui

import (
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/lenis2000/mrevdiff/pkg/build"
)

var (
	lmkfStatusDir = "/tmp/lmkf-status"
	lmkLocksDir   = "/tmp/lmk-locks"

	lmkfPollInterval = 300 * time.Millisecond
	// lmkfSettleWindow must exceed latexmk -pvc's watch interval (default
	// 2 s): if a completed pass had started before the edit, latexmk would
	// notice the newer source mtime and begin another pass within one
	// interval. A pass that stays the latest for this long therefore
	// includes the edit.
	lmkfSettleWindow = 5 * time.Second
)

// LmkfRebuildStatus is the outcome of waiting for LP's lmkf wrapper
// to finish the latexmk pass triggered by a source edit.
type LmkfRebuildStatus int

const (
	LmkfRebuildNotWatching LmkfRebuildStatus = iota
	LmkfRebuildOK
	LmkfRebuildError
	LmkfRebuildTimeout
)

// LmkfRebuildResult records the lmkf log-path handshake outcome.
type LmkfRebuildResult struct {
	Status    LmkfRebuildStatus
	LogPath   string
	ErrorLine string
}

// LmkfWatch describes a live lmkf watcher covering a project directory.
// MainTex is the file lmkf builds, which may differ from the reviewed
// file in a multi-file paper (the reviewed chapter is \input by MainTex).
type LmkfWatch struct {
	LogPath string
	MainTex string
}

// LmkfWatchFor returns the live lmkf watcher for texPath's directory, if
// any. The status file alone is not proof of life — a killed lmkf leaves
// it behind — so the watcher counts only when the lmk-guard lock for
// MainTex is held by a live process.
func LmkfWatchFor(texPath string) (LmkfWatch, bool) {
	abs, err := filepath.Abs(texPath)
	if err != nil {
		return LmkfWatch{}, false
	}
	projectDir := filepath.Dir(abs)
	data, err := os.ReadFile(filepath.Join(lmkfStatusDir, filepath.Base(projectDir)))
	if err != nil {
		return LmkfWatch{}, false
	}
	logPath := strings.TrimSpace(string(data))
	if logPath == "" || filepath.Ext(logPath) != ".log" {
		return LmkfWatch{}, false
	}
	// The status dir is keyed by directory basename only; a same-named
	// project elsewhere would collide. Only trust a log in our directory.
	if canonicalPath(filepath.Dir(logPath)) != canonicalPath(projectDir) {
		return LmkfWatch{}, false
	}
	mainTex := strings.TrimSuffix(logPath, ".log") + ".tex"
	if _, alive := lmkLockHolder(canonicalPath(mainTex)); !alive {
		return LmkfWatch{}, false
	}
	return LmkfWatch{LogPath: logPath, MainTex: mainTex}, true
}

// LmkfWatching reports whether LP's lmkf shell function is running
// latexmk -pvc on this exact .tex. When true, callers should skip
// invoking their own build — lmkf is already producing artefacts and
// a parallel latexmk would race on the build directory.
func LmkfWatching(texPath string) bool {
	watch, ok := LmkfWatchFor(texPath)
	if !ok {
		return false
	}
	abs, err := filepath.Abs(texPath)
	if err != nil {
		return false
	}
	return canonicalPath(watch.MainTex) == canonicalPath(abs)
}

// AwaitLmkfRebuild waits for lmkf's latexmk -pvc pass to finish and
// returns freshly rediscovered build artefact paths. When lmkf watches a
// different main file in the same project (multi-file paper), the wait
// and the artefacts both target that main file — latexmk tracks \input
// dependencies, so editing the reviewed subfile still triggers its pass.
func AwaitLmkfRebuild(texPath string, editTime time.Time, timeout time.Duration) (*build.Result, LmkfRebuildResult) {
	watch, ok := LmkfWatchFor(texPath)
	if !ok {
		return build.ResolveBuildOutputsOnDisk(texPath), LmkfRebuildResult{Status: LmkfRebuildNotWatching}
	}
	buildTex := watch.MainTex
	result, errLine := waitForLmkfComplete(watch.LogPath, editTime, timeout)
	res := build.ResolveBuildOutputsOnDisk(buildTex)
	switch result {
	case "ok":
		waitForArtefactsFresh(res.PDFPath, res.SyncTeXPath, editTime, 5*time.Second)
		res = build.ResolveBuildOutputsOnDisk(buildTex)
		return res, LmkfRebuildResult{Status: LmkfRebuildOK, LogPath: watch.LogPath}
	case "error":
		return res, LmkfRebuildResult{Status: LmkfRebuildError, LogPath: watch.LogPath, ErrorLine: errLine}
	default:
		return res, LmkfRebuildResult{Status: LmkfRebuildTimeout, LogPath: watch.LogPath}
	}
}

// latexmkCompleteMarker is the line latexmk prints at the end of every
// successful pdflatex pass. The menubar plugin at
// /Users/leo/menubar-plugins/lmkf-status.100ms.sh uses the same marker
// to distinguish "still compiling" from "finished".
const latexmkCompleteMarker = "Here is how much of TeX"

// waitForLmkfComplete polls the .log file until a pass that provably
// includes the edit made at `editTime` completes. A completed pass with
// log mtime >= editTime is not enough on its own: lmkf may have been
// mid-pass when the edit was saved, and that pass — reading the pre-edit
// source — also finishes after editTime. Two observations resolve the
// ambiguity:
//
//   - pdflatex truncates the log when a pass starts. Seeing the marker
//     disappear during the wait proves a pass started after the edit, so
//     its completion is trusted immediately.
//   - a completed pass that stays the latest for lmkfSettleWindow must
//     include the edit: latexmk -pvc would otherwise have noticed the
//     newer source mtime and started another pass within its interval.
//
// Returns ("ok", "") on success, ("error", firstErrorLine) when the
// accepted pass has a LaTeX error, or ("timeout", "") if the deadline
// expired with no completed pass at all.
func waitForLmkfComplete(logPath string, editTime time.Time, timeout time.Duration) (string, string) {
	deadline := time.Now().Add(timeout)
	prevMarker := false
	havePrev := false
	startedAfterEdit := false
	var settleUntil time.Time
	var settleMTime time.Time
	var candidate []byte
	for {
		var markerPresent bool
		var mtime time.Time
		var data []byte
		if st, err := os.Stat(logPath); err == nil {
			mtime = st.ModTime()
			if d, rerr := os.ReadFile(logPath); rerr == nil {
				data = d
				markerPresent = logContainsMarker(d)
			}
		}
		if havePrev && prevMarker && !markerPresent {
			startedAfterEdit = true
		}
		if markerPresent && !mtime.Before(editTime) {
			if startedAfterEdit {
				return finishLmkfPass(data)
			}
			candidate = data
			if settleUntil.IsZero() || !mtime.Equal(settleMTime) {
				settleUntil = time.Now().Add(lmkfSettleWindow)
				settleMTime = mtime
			} else if !time.Now().Before(settleUntil) {
				return finishLmkfPass(data)
			}
		} else {
			settleUntil = time.Time{}
		}
		prevMarker = markerPresent
		havePrev = true
		if !time.Now().Before(deadline) {
			// A candidate at the deadline is trustworthy for the same
			// settle reason: had it missed the edit, another pass would
			// have started (and been observed) long before a 2-minute
			// timeout ran out.
			if candidate != nil {
				return finishLmkfPass(candidate)
			}
			return "timeout", ""
		}
		time.Sleep(lmkfPollInterval)
	}
}

func finishLmkfPass(data []byte) (string, string) {
	if errLine := firstLogError(data); errLine != "" {
		return "error", errLine
	}
	return "ok", ""
}

// logContainsMarker reports whether the latexmk completion marker
// appears in the last 8 KiB of the log. latexmk appends on each pass,
// so checking the tail keeps the read cheap even for multi-thousand-
// line logs.
func logContainsMarker(data []byte) bool {
	const tailBytes = 8 * 1024
	if len(data) > tailBytes {
		data = data[len(data)-tailBytes:]
	}
	return strings.Contains(string(data), latexmkCompleteMarker)
}

// waitForArtefactsFresh polls pdfPath and synctexPath until each one's
// mtime is at least as recent as editTime, or the timeout elapses. The
// log-marker poll proves lmkf finished a pass; the artefact poll proves
// the resulting files are visible to the next pdf.Open / synctex.Open.
// Falls through silently on timeout — the caller has its own staleness
// fallback when the opens don't produce a coherent pair.
func waitForArtefactsFresh(pdfPath, synctexPath string, editTime time.Time, timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		ps, perr := os.Stat(pdfPath)
		ss, serr := os.Stat(synctexPath)
		if perr == nil && serr == nil &&
			!ps.ModTime().Before(editTime) && !ss.ModTime().Before(editTime) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// firstLogError surfaces the first TeX error or undefined-ref/citation
// warning from the log, delegating to build.ScanLogBytes so the lmkf
// path applies the same error policy as a direct build.RunWith call.
// Keeping the two scanners in sync matters because a "lmkf rebuild ok"
// message should never be shown for a state a manual rebuild would
// have rejected.
func firstLogError(data []byte) string {
	return build.ScanLogBytes(data)
}

// canonicalPath mirrors zsh's ${path:A} (absolute, symlinks resolved),
// which lmk-guard.sh hashes to name the lock directory.
func canonicalPath(p string) string {
	abs, err := filepath.Abs(p)
	if err != nil {
		return p
	}
	if resolved, rerr := filepath.EvalSymlinks(abs); rerr == nil {
		return resolved
	}
	return abs
}

func lmkLockPath(canonTex string) string {
	sum := md5.Sum([]byte(canonTex))
	return filepath.Join(lmkLocksDir, hex.EncodeToString(sum[:])+".lock")
}

// lmkLockHolder returns the label (`cmd` file) of the live process
// holding the lmk-guard lock for canonTex. alive=false means the lock is
// absent or its pid is dead.
func lmkLockHolder(canonTex string) (string, bool) {
	lock := lmkLockPath(canonTex)
	pidData, err := os.ReadFile(filepath.Join(lock, "pid"))
	if err != nil {
		return "", false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(pidData)))
	if err != nil || pid <= 0 || !processAlive(pid) {
		return "", false
	}
	cmd, _ := os.ReadFile(filepath.Join(lock, "cmd"))
	holder := strings.TrimSpace(string(cmd))
	if holder == "" {
		holder = "latexmk"
	}
	return holder, true
}

func processAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
}

// AcquireLmkBuildLock takes the lmk-guard lock for texPath before an
// own-latexmk run, exactly as lmk/lmkf/vim's \lx do. On success it
// returns a release func. When a live process already holds the lock it
// returns ok=false with the holder's label — the caller must not build.
// A lock left by a dead process is taken over.
func AcquireLmkBuildLock(texPath string) (release func(), holder string, ok bool) {
	canon := canonicalPath(texPath)
	lock := lmkLockPath(canon)
	if err := os.MkdirAll(lmkLocksDir, 0o755); err != nil {
		return func() {}, "", true // lock dir unavailable — build unguarded
	}
	for attempt := 0; attempt < 2; attempt++ {
		if err := os.Mkdir(lock, 0o755); err == nil {
			_ = os.WriteFile(filepath.Join(lock, "pid"), []byte(fmt.Sprintf("%d\n", os.Getpid())), 0o644)
			_ = os.WriteFile(filepath.Join(lock, "file"), []byte(canon+"\n"), 0o644)
			_ = os.WriteFile(filepath.Join(lock, "cmd"), []byte("mrevdiff\n"), 0o644)
			_ = os.WriteFile(filepath.Join(lock, "session"), []byte(os.Getenv("AGTERM_SESSION_ID")+"\n"), 0o644)
			return func() { _ = os.RemoveAll(lock) }, "", true
		}
		if h, alive := lmkLockHolder(canon); alive {
			return nil, h, false
		}
		_ = os.RemoveAll(lock)
	}
	return nil, "latexmk", false
}
