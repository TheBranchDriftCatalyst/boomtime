package climeta

// complete.go — the web-facing completion adapter for POST
// /api/v1/admin/cli/complete. The endpoint calls the registry's completion
// funcs DIRECTLY (never cobra's __complete argv dispatch — no argv is ever
// assembled from user input), threading the caller's prior positional values
// through so contextual completers (position-aware ones like
// CompleteUsernameThenRole) behave exactly as they do under a shell <TAB>.

import (
	"strings"

	"github.com/spf13/cobra"
)

// Suggestion is one completion candidate. Description carries the optional
// human hint cobra encodes after a "\t" in the raw candidate.
type Suggestion struct {
	Value       string `json:"value"`
	Description string `json:"description,omitempty"`
}

// Directive is cobra's ShellCompDirective bitmask decoded into named booleans
// for the FE. NoSort mirrors KeepOrder (cobra's name for "don't sort").
type Directive struct {
	NoFileComp bool `json:"noFileComp"`
	NoSpace    bool `json:"noSpace"`
	NoSort     bool `json:"noSort"`
	KeepOrder  bool `json:"keepOrder"`
	Error      bool `json:"error"`
}

// InvokeCompleter runs fn(nil, priorArgs, toComplete) and adapts the result
// to the wire shape. A nil fn (command/flag with no completer) yields no
// suggestions. A PANICKING completer must not crash the HTTP request — the
// recover() maps it to an empty result with the error directive set, the
// same fail-quiet posture the shell completers take on a DB error.
func InvokeCompleter(fn cobra.CompletionFunc, priorArgs []string, toComplete string) (suggestions []Suggestion, directive Directive) {
	suggestions = []Suggestion{}
	directive = Directive{NoFileComp: true}
	if fn == nil {
		return suggestions, directive
	}

	var raw []string
	var d cobra.ShellCompDirective
	func() {
		defer func() {
			if r := recover(); r != nil {
				raw, d = nil, cobra.ShellCompDirectiveError
			}
		}()
		raw, d = fn(nil, priorArgs, toComplete)
	}()

	for _, cand := range raw {
		value, desc, _ := strings.Cut(cand, "\t")
		suggestions = append(suggestions, Suggestion{Value: value, Description: desc})
	}
	return suggestions, decodeDirective(d)
}

// decodeDirective expands the cobra bitmask into the named booleans.
func decodeDirective(d cobra.ShellCompDirective) Directive {
	keep := d&cobra.ShellCompDirectiveKeepOrder != 0
	return Directive{
		NoFileComp: d&cobra.ShellCompDirectiveNoFileComp != 0,
		NoSpace:    d&cobra.ShellCompDirectiveNoSpace != 0,
		NoSort:     keep,
		KeepOrder:  keep,
		Error:      d&cobra.ShellCompDirectiveError != 0,
	}
}
