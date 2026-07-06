// lmkf integration: LP's lmkf shell wrapper runs `latexmk -pvc` on the
// side and owns the build pipeline. The wire protocol below is lmkf's
// external contract and must not be renamed: a status file at
// /tmp/lmkf-status/<basename-of-tex-dir> holds the absolute path of the
// .log file the watched latexmk is producing, and every completed
// pdflatex pass prints the marker "Here is how much of TeX" at the end
// of the log.
package ui

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	"mrevdiff/pkg/build"
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

// AwaitLmkfRebuild waits for lmkf's latexmk -pvc pass to finish and
// returns freshly rediscovered build artefact paths. It uses the same
// log-marker and error scanning policy as normal review reloads, and on
// success waits briefly for the PDF/SyncTeX files to become visible so
// callers do not open a stale pre-edit pair.
func AwaitLmkfRebuild(texPath string, editTime time.Time, timeout time.Duration) (*build.Result, LmkfRebuildResult) {
	res := build.ResolveBuildOutputsOnDisk(texPath)
	logPath, ok := lmkfLogPath(texPath)
	if !ok {
		return res, LmkfRebuildResult{Status: LmkfRebuildNotWatching}
	}
	result, errLine := waitForLmkfComplete(logPath, editTime, timeout)
	switch result {
	case "ok":
		res = build.ResolveBuildOutputsOnDisk(texPath)
		waitForArtefactsFresh(res.PDFPath, res.SyncTeXPath, editTime, 5*time.Second)
		res = build.ResolveBuildOutputsOnDisk(texPath)
		return res, LmkfRebuildResult{Status: LmkfRebuildOK, LogPath: logPath}
	case "error":
		res = build.ResolveBuildOutputsOnDisk(texPath)
		return res, LmkfRebuildResult{Status: LmkfRebuildError, LogPath: logPath, ErrorLine: errLine}
	default:
		res = build.ResolveBuildOutputsOnDisk(texPath)
		return res, LmkfRebuildResult{Status: LmkfRebuildTimeout, LogPath: logPath}
	}
}

// lmkfLogPath returns the absolute .log path lmkf is watching for
// this .tex, or ok=false if no matching status file exists.
func lmkfLogPath(texPath string) (string, bool) {
	abs, err := filepath.Abs(texPath)
	if err != nil {
		return "", false
	}
	projectDir := filepath.Dir(abs)
	statusFile := filepath.Join("/tmp/lmkf-status", filepath.Base(projectDir))
	data, err := os.ReadFile(statusFile)
	if err != nil {
		return "", false
	}
	want := strings.TrimSuffix(abs, filepath.Ext(abs)) + ".log"
	got := strings.TrimSpace(string(data))
	if got != want {
		return "", false
	}
	return got, true
}

// LmkfWatching reports whether LP's lmkf shell function is already
// running latexmk -pvc on this .tex. When true, callers should skip
// invoking their own build — lmkf is already producing artefacts and
// a parallel latexmk would race on the build directory.
func LmkfWatching(texPath string) bool {
	_, ok := lmkfLogPath(texPath)
	return ok
}

// latexmkCompleteMarker is the line latexmk prints at the end of every
// successful pdflatex pass. The menubar plugin at
// /Users/leo/menubar-plugins/lmkf-status.100ms.sh uses the same marker
// to distinguish "still compiling" from "finished".
const latexmkCompleteMarker = "Here is how much of TeX"

// waitForLmkfComplete polls the .log file until lmkf finishes a pass
// triggered by an edit made after `editTime`. Returns ("ok", "") on
// success, ("error", firstErrorLine) when the completed pass has a
// LaTeX error, or ("timeout", "") if the deadline expired (lmkf maybe
// not running fast enough, or not running at all). Polling the log is
// more reliable than comparing .pdf mtime — the PDF only updates on
// success, so failures would otherwise look like "stuck compiling"
// forever.
func waitForLmkfComplete(logPath string, editTime time.Time, timeout time.Duration) (string, string) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		st, err := os.Stat(logPath)
		if err == nil && !st.ModTime().Before(editTime) {
			if data, err := os.ReadFile(logPath); err == nil {
				if found := logContainsMarker(data); found {
					if errLine := firstLogError(data); errLine != "" {
						return "error", errLine
					}
					return "ok", ""
				}
			}
		}
		time.Sleep(300 * time.Millisecond)
	}
	return "timeout", ""
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
