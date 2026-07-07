package ui

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(body), 0o644))
}

// withChdir switches cwd to dir for the duration of t and restores it on
// cleanup. This lets tests control the ./.mrevdiff.toml probe.
func withChdir(t *testing.T, dir string) {
	t.Helper()
	prev, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(dir))
	t.Cleanup(func() { _ = os.Chdir(prev) })
}

// withEnvHome replaces HOME so the user-config probe lands in a test-owned
// directory. t.Setenv restores the original on cleanup.
func withEnvHome(t *testing.T, dir string) {
	t.Helper()
	t.Setenv("HOME", dir)
}

func TestLoadConfig_NoFiles(t *testing.T) {
	dir := t.TempDir()
	withChdir(t, dir)
	withEnvHome(t, dir)
	cfg, err := LoadConfig("", false)
	require.NoError(t, err)
	require.NotNil(t, cfg)
	assert.Empty(t, cfg.TheoremEnvs)
	assert.Empty(t, cfg.BuildCmd)
	assert.Empty(t, cfg.Theme)
	assert.NotNil(t, cfg.Colors)
	assert.NotNil(t, cfg.Keybinds)
}

func TestLoadConfig_UserOnly(t *testing.T) {
	dir := t.TempDir()
	withChdir(t, dir)
	withEnvHome(t, dir)
	writeFile(t, filepath.Join(dir, ".config", "mrevdiff", "config.toml"), `
theorem_envs = ["lemma", "claim"]
build_cmd = "pdflatex"
theme = "dark"

[colors]
status = "orange"

[keybinds]
quit = "ZZ"
`)
	cfg, err := LoadConfig("", false)
	require.NoError(t, err)
	assert.Equal(t, []string{"lemma", "claim"}, cfg.TheoremEnvs)
	assert.Equal(t, "pdflatex", cfg.BuildCmd)
	assert.Equal(t, "dark", cfg.Theme)
	assert.Equal(t, "orange", cfg.Colors["status"])
	assert.Equal(t, "ZZ", cfg.Keybinds["quit"])
}

func TestLoadConfig_ProjectOverridesUser(t *testing.T) {
	dir := t.TempDir()
	withChdir(t, dir)
	withEnvHome(t, dir)
	writeFile(t, filepath.Join(dir, ".config", "mrevdiff", "config.toml"), `
theorem_envs = ["lemma"]
build_cmd = "latexmk"
theme = "dark"

[colors]
status = "orange"
cursor = "red"

[keybinds]
quit = "q"
`)
	writeFile(t, filepath.Join(dir, ".mrevdiff.toml"), `
theorem_envs = ["corollary"]
build_cmd = "pdflatex"

[colors]
status = "blue"

[keybinds]
quit = "ZZ"
`)
	cfg, err := LoadConfig("", false)
	require.NoError(t, err)
	// Slices replaced wholesale.
	assert.Equal(t, []string{"corollary"}, cfg.TheoremEnvs)
	// Scalar overridden.
	assert.Equal(t, "pdflatex", cfg.BuildCmd)
	// Theme not overridden in project -> user wins.
	assert.Equal(t, "dark", cfg.Theme)
	// Maps merged: project key overrides, user-only key survives.
	assert.Equal(t, "blue", cfg.Colors["status"])
	assert.Equal(t, "red", cfg.Colors["cursor"])
	assert.Equal(t, "ZZ", cfg.Keybinds["quit"])
}

func TestLoadConfig_ExplicitOverridesAllLayers(t *testing.T) {
	dir := t.TempDir()
	withChdir(t, dir)
	withEnvHome(t, dir)
	// Lay down user and project files that must NOT be loaded.
	writeFile(t, filepath.Join(dir, ".config", "mrevdiff", "config.toml"), `build_cmd = "user"`)
	writeFile(t, filepath.Join(dir, ".mrevdiff.toml"), `build_cmd = "project"`)
	explicit := filepath.Join(dir, "custom.toml")
	writeFile(t, explicit, `build_cmd = "explicit"
theme = "light"
`)
	cfg, err := LoadConfig(explicit, false)
	require.NoError(t, err)
	assert.Equal(t, "explicit", cfg.BuildCmd)
	assert.Equal(t, "light", cfg.Theme)
}

func TestLoadConfig_ExplicitMissingIsError(t *testing.T) {
	_, err := LoadConfig(filepath.Join(t.TempDir(), "does-not-exist.toml"), false)
	require.Error(t, err)
}

func TestLoadConfig_MalformedIsError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.toml")
	writeFile(t, path, `theorem_envs = [this is not toml`)
	_, err := LoadConfig(path, false)
	require.Error(t, err)
}

func TestApplyThemeEnv(t *testing.T) {
	t.Run("sets dark", func(t *testing.T) {
		t.Setenv("MREVDIFF_THEME", "dark")
		cfg := ApplyThemeEnv(DefaultConfig())
		assert.Equal(t, "dark", cfg.Theme)
	})
	t.Run("sets light", func(t *testing.T) {
		t.Setenv("MREVDIFF_THEME", "Light")
		cfg := ApplyThemeEnv(DefaultConfig())
		assert.Equal(t, "light", cfg.Theme)
	})
	t.Run("ignores unknown", func(t *testing.T) {
		t.Setenv("MREVDIFF_THEME", "solarized")
		cfg := DefaultConfig()
		cfg.Theme = "dark"
		cfg = ApplyThemeEnv(cfg)
		assert.Equal(t, "dark", cfg.Theme)
	})
	t.Run("nil config is safe", func(t *testing.T) {
		t.Setenv("MREVDIFF_THEME", "dark")
		cfg := ApplyThemeEnv(nil)
		require.NotNil(t, cfg)
		assert.Equal(t, "dark", cfg.Theme)
	})
}

func TestStylesForTheme(t *testing.T) {
	// Light and dark both return non-empty palettes; they are distinct.
	dark := StylesForTheme("dark")
	light := StylesForTheme("light")
	fallback := StylesForTheme("solarized")
	assert.NotEqual(t, dark.StatusBar, light.StatusBar, "themes should render different status bars")
	assert.Equal(t, dark.StatusBar, fallback.StatusBar, "unknown theme falls back to dark default")
}

func TestMergeConfig_SliceReplacementNotAppended(t *testing.T) {
	base := DefaultConfig()
	base.TheoremEnvs = []string{"a", "b"}
	overlay := &Config{TheoremEnvs: []string{"c"}}
	mergeConfig(base, overlay)
	assert.Equal(t, []string{"c"}, base.TheoremEnvs)
}

func TestLoadConfig_NoConfigSkipsAllLayers(t *testing.T) {
	dir := t.TempDir()
	withChdir(t, dir)
	withEnvHome(t, dir)
	writeFile(t, filepath.Join(dir, ".config", "mrevdiff", "config.toml"), `build_cmd = "user"`)
	writeFile(t, filepath.Join(dir, ".mrevdiff.toml"), `build_cmd = "project"`)
	cfg, err := LoadConfig("", true)
	require.NoError(t, err)
	assert.Empty(t, cfg.BuildCmd, "noconfig must ignore both user and project files")
}

func TestLoadConfig_ProjectDiscoveryWalksUp(t *testing.T) {
	root := t.TempDir()
	withEnvHome(t, root)

	require.NoError(t, os.Mkdir(filepath.Join(root, ".git"), 0o755))
	writeFile(t, filepath.Join(root, ".mrevdiff.toml"), `build_cmd = "found"`)
	deep := filepath.Join(root, "sub", "sub2")
	require.NoError(t, os.MkdirAll(deep, 0o755))
	withChdir(t, deep)

	cfg, err := LoadConfig("", false)
	require.NoError(t, err)
	assert.Equal(t, "found", cfg.BuildCmd, "should discover .mrevdiff.toml at the git root")
}

func TestLoadConfig_ProjectDiscoveryStopsAtGitRoot(t *testing.T) {
	// Outer dir has a .mrevdiff.toml. Inner dir is a git repo (with .git but
	// no .mrevdiff.toml). Discovery from inner must stop at the git root and
	// must NOT find the outer config.
	outer := t.TempDir()
	withEnvHome(t, outer)
	writeFile(t, filepath.Join(outer, ".mrevdiff.toml"), `build_cmd = "outer"`)
	inner := filepath.Join(outer, "inner")
	require.NoError(t, os.MkdirAll(filepath.Join(inner, ".git"), 0o755))
	withChdir(t, inner)

	cfg, err := LoadConfig("", false)
	require.NoError(t, err)
	assert.Empty(t, cfg.BuildCmd, "discovery must stop at the inner git root")
}
