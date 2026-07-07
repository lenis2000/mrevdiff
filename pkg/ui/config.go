package ui

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

// Config is the merged user+project configuration. All fields are optional
// and only override the defaults when populated.
//
// The precedence rule is: project (.mrevdiff.toml found by walking up from
// cwd to the git root) overrides user (~/.config/mrevdiff/config.toml),
// and an explicit `--config` replaces both layers entirely.
type Config struct {
	// Parser overrides.
	TheoremEnvs []string `toml:"theorem_envs"`
	FigureEnvs  []string `toml:"figure_envs"`

	// Build override — empty string uses the built-in latexmk invocation.
	BuildCmd string `toml:"build_cmd"`

	// UI overrides.
	Theme  string            `toml:"theme"`
	Colors map[string]string `toml:"colors"`
	// Keybinds maps a key to an action name (e.g. ";" = "next"); it is a
	// convenience overlay applied before the keybindings file. See
	// `mrevdiff --dump-keys` for the action catalog. (Colors remains
	// parsed but unused.)
	Keybinds map[string]string `toml:"keybinds"`

	// `[fmt]` sub-table — controls `mrevdiff fmt` defaults.
	Fmt FmtConfig `toml:"fmt"`
}

// FmtConfig holds `[fmt]` sub-table values. Pointer-to-bool distinguishes
// "unset" from "set to false" so layered merges and flag-override work.
type FmtConfig struct {
	NoPDFFix      *bool             `toml:"no_pdf_fix"`
	VerifyPDF     string            `toml:"verify_pdf"` // "" | "text" | "visual"
	NoVerify      *bool             `toml:"no_verify"`
	NoReport      *bool             `toml:"no_report"`
	VerbatimEnvs  []string          `toml:"verbatim_envs"`
	Indent        *bool             `toml:"indent"`      // default true
	IndentChar    string            `toml:"indent_char"` // "" | "space" | "tab"
	IndentSize    int               `toml:"indent_size"`
	IndentRules   map[string]string `toml:"indent_rules"` // per-env indent override; "" = no indent, non-empty = literal indent unit
	Wrap          string            `toml:"wrap"`         // "" | "off" | "column" | "sentence" | "sentence+column"
	WrapCol       int               `toml:"wrap_col"`
	TildeRefs     []string          `toml:"tilde_refs"`      // cite/ref commands for prose.tilde-refs; omit = defaults
	MathAlign     *bool             `toml:"math_align"`      // default true; set false to disable math.align-columns
	MathAlignEnvs []string          `toml:"math_align_envs"` // override default env list for math.align-columns
	MathAlignSkip []string          `toml:"math_align_skip"` // envs to skip even if in the align list
	MathWrap      *bool             `toml:"math_wrap"`       // default false (opt-in); set true to enable math.wrap-at-break-op
	MathWrapCol   int               `toml:"math_wrap_col"`   // target column for math.wrap-at-break-op; 0 = 80
	SkipRules     []string          `toml:"skip_rules"`      // disable these rule IDs even when otherwise enabled
}

// DefaultConfig returns an empty Config with non-nil maps so callers can
// merge layer-by-layer without nil-guarding every key access.
func DefaultConfig() *Config {
	return &Config{
		Colors:   map[string]string{},
		Keybinds: map[string]string{},
	}
}

// LoadConfig resolves the configuration layer stack.
//
//	noConfig=true  -> return defaults; ignore explicit, user, project
//	explicit != "" -> load that file exclusively (missing file is an error)
//	explicit == "" -> merge ~/.config/mrevdiff/config.toml (if any), then
//	                  the nearest .mrevdiff.toml found by walking from the
//	                  current working directory up to the git root (if any).
//	                  Missing files are silently ignored.
func LoadConfig(explicit string, noConfig bool) (*Config, error) {
	cfg := DefaultConfig()
	if noConfig {
		return cfg, nil
	}
	if explicit != "" {
		if err := applyConfigFile(cfg, explicit, true); err != nil {
			return nil, err
		}
		return cfg, nil
	}
	if home, err := os.UserHomeDir(); err == nil {
		userPath := filepath.Join(home, ".config", "mrevdiff", "config.toml")
		if err := applyConfigFile(cfg, userPath, false); err != nil {
			return nil, err
		}
	}
	if path := FindProjectConfig(); path != "" {
		if err := applyConfigFile(cfg, path, false); err != nil {
			return nil, err
		}
	}
	return cfg, nil
}

// FindProjectConfig walks upward from the current working directory looking
// for a `.mrevdiff.toml` file. The search stops at the first match, at the
// containing git root, or at the filesystem root — whichever comes first.
// Returns the absolute path to the file, or "" when no config was found.
func FindProjectConfig() string {
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}
	dir, _ = filepath.Abs(dir)
	for {
		candidate := filepath.Join(dir, ".mrevdiff.toml")
		if st, err := os.Stat(candidate); err == nil && !st.IsDir() {
			return candidate
		}
		// Stop at git root.
		if st, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			_ = st
			return ""
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "" // reached filesystem root
		}
		dir = parent
	}
}

// applyConfigFile decodes path into an overlay and merges it into cfg.
// strict=true turns a missing file into an error; otherwise it is ignored.
func applyConfigFile(cfg *Config, path string, strict bool) error {
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			if strict {
				return fmt.Errorf("config %q: %w", path, err)
			}
			return nil
		}
		return err
	}
	var overlay Config
	if _, err := toml.DecodeFile(path, &overlay); err != nil {
		return fmt.Errorf("config %q: %w", path, err)
	}
	mergeConfig(cfg, &overlay)
	return nil
}

// mergeConfig applies any fields set in overlay on top of base. Slices are
// replaced wholesale (not appended); scalars are replaced when non-zero; maps
// merge per-key.
func mergeConfig(base, overlay *Config) {
	if base == nil || overlay == nil {
		return
	}
	if len(overlay.TheoremEnvs) > 0 {
		base.TheoremEnvs = append([]string(nil), overlay.TheoremEnvs...)
	}
	if len(overlay.FigureEnvs) > 0 {
		base.FigureEnvs = append([]string(nil), overlay.FigureEnvs...)
	}
	if overlay.BuildCmd != "" {
		base.BuildCmd = overlay.BuildCmd
	}
	if overlay.Theme != "" {
		base.Theme = overlay.Theme
	}
	if base.Colors == nil {
		base.Colors = map[string]string{}
	}
	for k, v := range overlay.Colors {
		base.Colors[k] = v
	}
	if base.Keybinds == nil {
		base.Keybinds = map[string]string{}
	}
	for k, v := range overlay.Keybinds {
		base.Keybinds[k] = v
	}
	mergeFmtConfig(&base.Fmt, &overlay.Fmt)
}

// mergeFmtConfig merges the [fmt] sub-table. Pointer-to-bool fields override
// when set in the overlay; string/int fields override when non-empty/non-zero;
// slice fields replace wholesale when the overlay slice is non-empty.
func mergeFmtConfig(base, overlay *FmtConfig) {
	if overlay.NoPDFFix != nil {
		v := *overlay.NoPDFFix
		base.NoPDFFix = &v
	}
	if overlay.NoVerify != nil {
		v := *overlay.NoVerify
		base.NoVerify = &v
	}
	if overlay.NoReport != nil {
		v := *overlay.NoReport
		base.NoReport = &v
	}
	if overlay.Indent != nil {
		v := *overlay.Indent
		base.Indent = &v
	}
	if overlay.VerifyPDF != "" {
		base.VerifyPDF = overlay.VerifyPDF
	}
	if overlay.IndentChar != "" {
		base.IndentChar = overlay.IndentChar
	}
	if overlay.Wrap != "" {
		base.Wrap = overlay.Wrap
	}
	if overlay.IndentSize > 0 {
		base.IndentSize = overlay.IndentSize
	}
	if overlay.WrapCol > 0 {
		base.WrapCol = overlay.WrapCol
	}
	if len(overlay.VerbatimEnvs) > 0 {
		base.VerbatimEnvs = append([]string(nil), overlay.VerbatimEnvs...)
	}
	if len(overlay.IndentRules) > 0 {
		if base.IndentRules == nil {
			base.IndentRules = map[string]string{}
		}
		for k, v := range overlay.IndentRules {
			base.IndentRules[k] = v
		}
	}
	if len(overlay.TildeRefs) > 0 {
		base.TildeRefs = append([]string(nil), overlay.TildeRefs...)
	}
	if overlay.MathAlign != nil {
		v := *overlay.MathAlign
		base.MathAlign = &v
	}
	if len(overlay.MathAlignEnvs) > 0 {
		base.MathAlignEnvs = append([]string(nil), overlay.MathAlignEnvs...)
	}
	if len(overlay.MathAlignSkip) > 0 {
		base.MathAlignSkip = append([]string(nil), overlay.MathAlignSkip...)
	}
	if overlay.MathWrap != nil {
		v := *overlay.MathWrap
		base.MathWrap = &v
	}
	if overlay.MathWrapCol > 0 {
		base.MathWrapCol = overlay.MathWrapCol
	}
	if len(overlay.SkipRules) > 0 {
		base.SkipRules = append([]string(nil), overlay.SkipRules...)
	}
}

// ApplyThemeEnv returns cfg with its Theme replaced by MREVDIFF_THEME when
// the env var is set to
// "dark" or "light". Other values are ignored so a stray setting does not
// clobber a user-configured theme.
func ApplyThemeEnv(cfg *Config) *Config {
	if cfg == nil {
		cfg = DefaultConfig()
	}
	v := strings.ToLower(os.Getenv("MREVDIFF_THEME"))
	if v == "dark" || v == "light" {
		cfg.Theme = v
	}
	return cfg
}

// StylesForTheme returns a Styles palette for the given theme label. Unknown
// labels (including the empty string) fall back to DefaultStyles.
func StylesForTheme(theme string) Styles {
	switch strings.ToLower(theme) {
	case "light":
		return lightStyles()
	case "dark":
		return DefaultStyles()
	}
	return DefaultStyles()
}
