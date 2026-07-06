package ui

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestKittyGraphicsAvailable guards the env-var heuristics that
// decide whether we emit APC graphics sequences at all. A regression
// here would either (a) paint raw escape garbage in non-kitty
// terminals, or (b) silently downgrade the PDF pane to a placeholder
// on terminals that *do* support kitty.
func TestKittyGraphicsAvailable(t *testing.T) {
	cases := []struct {
		name string
		env  map[string]string
		want bool
	}{
		{
			name: "no kitty env — nothing to indicate support",
			env:  map[string]string{},
			want: false,
		},
		{
			name: "kitty terminal sets KITTY_WINDOW_ID",
			env:  map[string]string{"KITTY_WINDOW_ID": "1"},
			want: true,
		},
		{
			name: "ghostty terminal",
			env:  map[string]string{"GHOSTTY_RESOURCES_DIR": "/opt/ghostty"},
			want: true,
		},
		{
			name: "kitty under tmux — disabled by default",
			env:  map[string]string{"KITTY_WINDOW_ID": "1", "TMUX": "/tmp/tmux-0"},
			want: false,
		},
		{
			name: "force-enable override under tmux",
			env: map[string]string{
				"TMUX":                 "/tmp/tmux-0",
				"KITTY_WINDOW_ID":      "1",
				"MREVDIFF_FORCE_KITTY": "1",
			},
			want: true,
		},
		{
			name: "force-enable without any other indicator",
			env:  map[string]string{"MREVDIFF_FORCE_KITTY": "1"},
			want: true,
		},
		{
			name: "mreview compat force-enable still honoured",
			env:  map[string]string{"MREVIEW_FORCE_KITTY": "1"},
			want: true,
		},
	}
	// Env vars we might touch — clear them all before each subtest so
	// ambient shell state from the dev box doesn't bleed in.
	envKeys := []string{
		"KITTY_WINDOW_ID",
		"GHOSTTY_RESOURCES_DIR",
		"TMUX",
		"MREVDIFF_FORCE_KITTY",
		"MREVIEW_FORCE_KITTY",
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, k := range envKeys {
				t.Setenv(k, "")
			}
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			assert.Equal(t, tc.want, KittyGraphicsAvailable())
		})
	}
}
