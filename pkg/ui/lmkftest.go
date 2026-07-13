package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// FakeLmkfWatcherForTest registers texPath as lmkf-watched the way the
// real wrapper does — status file plus a lmk-guard lock held by this
// process — and returns a cleanup func. Test hook only; it lives outside
// _test.go so other packages' tests can fake the full wire protocol
// without duplicating the lock-hash details.
func FakeLmkfWatcherForTest(texPath string) (func(), error) {
	abs, err := filepath.Abs(texPath)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(lmkfStatusDir, 0o755); err != nil {
		return nil, err
	}
	statusFile := filepath.Join(lmkfStatusDir, filepath.Base(filepath.Dir(abs)))
	logPath := strings.TrimSuffix(abs, filepath.Ext(abs)) + ".log"
	if err := os.WriteFile(statusFile, []byte(logPath+"\n"), 0o600); err != nil {
		return nil, err
	}
	lock := lmkLockPath(canonicalPath(abs))
	if err := os.MkdirAll(lock, 0o755); err != nil {
		_ = os.Remove(statusFile)
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(lock, "pid"), []byte(fmt.Sprintf("%d\n", os.Getpid())), 0o644); err != nil {
		_ = os.Remove(statusFile)
		_ = os.RemoveAll(lock)
		return nil, err
	}
	_ = os.WriteFile(filepath.Join(lock, "cmd"), []byte("lmkf\n"), 0o644)
	return func() {
		_ = os.Remove(statusFile)
		_ = os.RemoveAll(lock)
	}, nil
}

// SetLmkfWaitTimingsForTest shrinks the wait loop's poll interval and
// settle window so tests exercising waitForLmkfComplete run fast.
// Returns a restore func. Test hook only.
func SetLmkfWaitTimingsForTest(poll, settle time.Duration) func() {
	origPoll, origSettle := lmkfPollInterval, lmkfSettleWindow
	lmkfPollInterval, lmkfSettleWindow = poll, settle
	return func() {
		lmkfPollInterval, lmkfSettleWindow = origPoll, origSettle
	}
}
