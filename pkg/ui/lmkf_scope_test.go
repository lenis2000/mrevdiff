package ui

import (
	"os"
	"path/filepath"
	"testing"
)

// TestTexPartOfBuild pins the fix for lmkf's directory-scoped watcher: the
// status file is keyed by directory, so every .tex beside a watched main file
// looked watched. A sibling the main file never \inputs (referee response,
// slides) would then adopt the main file's PDF and block every rebuild.
func TestTexPartOfBuild(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "paper.log")
	mainTex := filepath.Join(dir, "paper.tex")
	chapter := filepath.Join(dir, "chapter1.tex")
	sibling := filepath.Join(dir, "response.tex")

	// latexmk's .fls records what the pass actually read.
	fls := "PWD " + dir + "\n" +
		"INPUT " + mainTex + "\n" +
		"INPUT chapter1.tex\n" +
		"OUTPUT paper.pdf\n"
	if err := os.WriteFile(filepath.Join(dir, "paper.fls"), []byte(fls), 0o644); err != nil {
		t.Fatal(err)
	}

	if !texPartOfBuild(logPath, mainTex, mainTex) {
		t.Error("the watched main file itself must count as part of the build")
	}
	if !texPartOfBuild(logPath, mainTex, chapter) {
		t.Error("an \\input dependency listed in the .fls must count as part of the build")
	}
	if texPartOfBuild(logPath, mainTex, sibling) {
		t.Error("a sibling .tex absent from the .fls must NOT be treated as part of the watched build")
	}
}

// TestTexPartOfBuildWithoutFLS pins the conservative fallback: while the first
// pass is still running there is no .fls yet, and assuming independence there
// would send a multi-file paper's chapter off to compile standalone.
func TestTexPartOfBuildWithoutFLS(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "paper.log")
	mainTex := filepath.Join(dir, "paper.tex")
	chapter := filepath.Join(dir, "chapter1.tex")

	if !texPartOfBuild(logPath, mainTex, chapter) {
		t.Error("with no .fls to consult, the reviewed file must still be assumed part of the build")
	}
}
