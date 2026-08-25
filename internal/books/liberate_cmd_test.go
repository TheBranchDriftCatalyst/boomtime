package books

import (
	"bytes"
	"strings"
	"testing"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/climeta"
)

// The guard clauses run BEFORE any database or config work, so they are unit
// testable — and they are worth testing because each one exists to stop a
// specific expensive mistake.
func TestLiberateCmdArgumentGuards(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		wantErr string
	}{
		{"no user", []string{"B0TEST"}, "--user is required"},
		{"no target", []string{"--user", "alice"}, "give an ASIN, or --all"},
		{"both asin and all", []string{"B0TEST", "--user", "alice", "--all"}, "not both"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cmd := NewLiberateCmd()
			cmd.SetOut(&bytes.Buffer{})
			cmd.SetErr(&bytes.Buffer{})
			cmd.SetArgs(tc.args)

			err := cmd.Execute()
			if err == nil {
				t.Fatal("want an error")
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("err = %q, want it to contain %q", err, tc.wantErr)
			}
		})
	}
}

// Completion is a standing convention for every command that takes an entity or
// enum argument, so its absence is a defect rather than a missing nicety.
func TestLiberateCmdsWireCompletion(t *testing.T) {
	lib := NewLiberateCmd()
	if lib.ValidArgsFunction == nil {
		t.Error("liberate: positional ASIN has no completion function")
	}
	if lib.Flags().Lookup("user") == nil {
		t.Fatal("liberate: no --user flag")
	}
	status := NewLiberationStatusCmd()
	if status.ValidArgsFunction == nil {
		t.Error("liberation-status: no ValidArgsFunction (should at least disable file completion)")
	}
	if status.Flags().Lookup("user") == nil {
		t.Error("liberation-status: no --user flag")
	}

	// The web CLI-runner classifies commands by annotation; an unclassified
	// command is invisible to it.
	if lib.Annotations[climeta.WebAnnotation] != climeta.ClassMutating {
		t.Errorf("liberate annotation = %q, want %q", lib.Annotations[climeta.WebAnnotation], climeta.ClassMutating)
	}
	if status.Annotations[climeta.WebAnnotation] != climeta.ClassReadonly {
		t.Errorf("liberation-status annotation = %q, want %q", status.Annotations[climeta.WebAnnotation], climeta.ClassReadonly)
	}
}

func TestHumanBytes(t *testing.T) {
	cases := map[int64]string{
		0: "0 B", 512: "512 B", 1024: "1.0 KB",
		1536: "1.5 KB", 1048576: "1.0 MB", 629145600: "600.0 MB",
	}
	for in, want := range cases {
		if got := humanBytes(in); got != want {
			t.Errorf("humanBytes(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestOrNone(t *testing.T) {
	if got := orNone("   "); got != "(not configured)" {
		t.Errorf("orNone(blank) = %q", got)
	}
	if got := orNone("/media/audiobooks/liberated"); got != "/media/audiobooks/liberated" {
		t.Errorf("orNone(path) = %q", got)
	}
}
