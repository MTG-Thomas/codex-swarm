package userpath

import (
	"strings"
	"testing"
)

func TestApply(t *testing.T) {
	longPrefix := strings.Repeat(`C:\very-long-path-segment;`, 80)

	tests := []struct {
		name       string
		current    string
		entry      string
		action     Action
		wantPath   string
		wantResult Result
	}{
		{
			name:       "add to empty path",
			entry:      `C:\Tools\codex-swarm`,
			action:     Add,
			wantPath:   `C:\Tools\codex-swarm`,
			wantResult: Added,
		},
		{
			name:       "add preserves path longer than NSIS buffer",
			current:    longPrefix,
			entry:      `C:\Tools\codex-swarm`,
			action:     Add,
			wantPath:   longPrefix + `C:\Tools\codex-swarm`,
			wantResult: Added,
		},
		{
			name:       "add preserves existing trailing separator",
			current:    `C:\One;C:\Two;`,
			entry:      `C:\Tools\codex-swarm`,
			action:     Add,
			wantPath:   `C:\One;C:\Two;C:\Tools\codex-swarm`,
			wantResult: Added,
		},
		{
			name:       "add recognizes quoted case-insensitive entry",
			current:    `C:\One;"c:\tools\CODEX-SWARM\\";C:\Two`,
			entry:      `C:\Tools\codex-swarm`,
			action:     Add,
			wantPath:   `C:\One;"c:\tools\CODEX-SWARM\\";C:\Two`,
			wantResult: Present,
		},
		{
			name:       "remove from long path preserves other entries",
			current:    longPrefix + `C:\Tools\codex-swarm;C:\Last`,
			entry:      `C:\Tools\codex-swarm`,
			action:     Remove,
			wantPath:   longPrefix + `C:\Last`,
			wantResult: Removed,
		},
		{
			name:       "remove all equivalent entries",
			current:    `C:\One;"C:\Tools\codex-swarm";c:\tools\CODEX-SWARM\\;C:\Two`,
			entry:      `C:\Tools\codex-swarm`,
			action:     Remove,
			wantPath:   `C:\One;C:\Two`,
			wantResult: Removed,
		},
		{
			name:       "remove absent entry changes nothing",
			current:    `C:\One;;C:\Two;`,
			entry:      `C:\Tools\codex-swarm`,
			action:     Remove,
			wantPath:   `C:\One;;C:\Two;`,
			wantResult: Absent,
		},
		{
			name:       "remove only entry empties path",
			current:    `C:\Tools\codex-swarm`,
			entry:      `C:\Tools\codex-swarm`,
			action:     Remove,
			wantPath:   "",
			wantResult: Removed,
		},
		{
			name:       "remove collapses separator-only path",
			current:    `;C:\Tools\codex-swarm;`,
			entry:      `C:\Tools\codex-swarm`,
			action:     Remove,
			wantPath:   "",
			wantResult: Removed,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			gotPath, gotResult, err := Apply(test.current, test.entry, test.action)
			if err != nil {
				t.Fatalf("Apply() error = %v", err)
			}
			if gotPath != test.wantPath {
				t.Fatalf("Apply() path = %q, want %q", gotPath, test.wantPath)
			}
			if gotResult != test.wantResult {
				t.Fatalf("Apply() result = %q, want %q", gotResult, test.wantResult)
			}
		})
	}
}

func TestApplyRejectsInvalidInput(t *testing.T) {
	if _, _, err := Apply(`C:\One`, "", Add); err == nil {
		t.Fatal("Apply() accepted an empty entry")
	}
	if _, _, err := Apply(`C:\One`, `C:\Tools`, Action("replace")); err == nil {
		t.Fatal("Apply() accepted an unsupported action")
	}
}
