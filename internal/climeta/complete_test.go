package climeta_test

// complete_test.go — the web completion adapter: prior-args threading (the
// contextual-completion contract), tab-description splitting, directive
// decoding, nil-completer handling, and panic containment.

import (
	"reflect"
	"testing"

	"github.com/spf13/cobra"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/climeta"
)

func TestInvokeCompleterThreadsPriorArgsAndToComplete(t *testing.T) {
	var gotArgs []string
	var gotToComplete string
	fn := cobra.CompletionFunc(func(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		gotArgs, gotToComplete = args, toComplete
		return []string{"alice", "albert"}, cobra.ShellCompDirectiveNoFileComp
	})

	prior := []string{"first-positional", "second"}
	suggestions, directive := climeta.InvokeCompleter(fn, prior, "al")

	if !reflect.DeepEqual(gotArgs, prior) {
		t.Errorf("prior positional args not threaded: got %v want %v", gotArgs, prior)
	}
	if gotToComplete != "al" {
		t.Errorf("toComplete not threaded: got %q", gotToComplete)
	}
	if len(suggestions) != 2 || suggestions[0].Value != "alice" {
		t.Errorf("suggestions wrong: %+v", suggestions)
	}
	if !directive.NoFileComp || directive.Error {
		t.Errorf("directive wrong: %+v", directive)
	}
}

func TestInvokeCompleterSplitsTabDescriptions(t *testing.T) {
	fn := cobra.CompletionFunc(func(_ *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
		return []string{"polyglot\t🗣 Polyglot", "bare"}, cobra.ShellCompDirectiveNoFileComp | cobra.ShellCompDirectiveKeepOrder
	})
	suggestions, directive := climeta.InvokeCompleter(fn, nil, "")
	if suggestions[0].Value != "polyglot" || suggestions[0].Description != "🗣 Polyglot" {
		t.Errorf("tab-description not split: %+v", suggestions[0])
	}
	if suggestions[1].Value != "bare" || suggestions[1].Description != "" {
		t.Errorf("bare candidate mangled: %+v", suggestions[1])
	}
	if !directive.KeepOrder || !directive.NoSort {
		t.Errorf("KeepOrder must decode into both keepOrder and noSort: %+v", directive)
	}
}

func TestInvokeCompleterNilFn(t *testing.T) {
	suggestions, directive := climeta.InvokeCompleter(nil, nil, "x")
	if suggestions == nil || len(suggestions) != 0 {
		t.Errorf("nil completer must yield an empty (non-nil) suggestion list: %#v", suggestions)
	}
	if !directive.NoFileComp {
		t.Errorf("nil completer directive must still suppress file completion: %+v", directive)
	}
}

func TestInvokeCompleterRecoversPanic(t *testing.T) {
	fn := cobra.CompletionFunc(func(_ *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
		panic("completer bug")
	})
	suggestions, directive := climeta.InvokeCompleter(fn, nil, "")
	if len(suggestions) != 0 {
		t.Errorf("panicking completer must yield no suggestions: %+v", suggestions)
	}
	if !directive.Error {
		t.Errorf("panicking completer must set the error directive: %+v", directive)
	}
}

func TestInvokeCompleterDirectiveBits(t *testing.T) {
	mk := func(d cobra.ShellCompDirective) climeta.Directive {
		fn := cobra.CompletionFunc(func(_ *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
			return nil, d
		})
		_, directive := climeta.InvokeCompleter(fn, nil, "")
		return directive
	}
	if d := mk(cobra.ShellCompDirectiveNoSpace); !d.NoSpace || d.NoFileComp {
		t.Errorf("NoSpace decode wrong: %+v", d)
	}
	if d := mk(cobra.ShellCompDirectiveError); !d.Error {
		t.Errorf("Error decode wrong: %+v", d)
	}
	if d := mk(cobra.ShellCompDirectiveDefault); d.NoFileComp || d.NoSpace || d.Error || d.KeepOrder {
		t.Errorf("Default decode wrong: %+v", d)
	}
}
