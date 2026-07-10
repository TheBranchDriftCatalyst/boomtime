// Package wakatime holds Wakatime-client-specific helpers: user-agent parsing
// (Utils.userAgentInfo) and file-extension -> language detection (Heartbeats.addMissingLang).
package wakatime

import (
	"path/filepath"
	"strings"
)

// EditorInfo is the parsed user-agent (Utils.EditorInfo).
type EditorInfo struct {
	Editor   *string
	Plugin   *string
	Platform *string
}

// UserAgentInfo splits the user agent on spaces and extracts:
// platform=tokens[1], editor=tokens[3], plugin=tokens[4] (Utils.userAgentInfo).
func UserAgentInfo(ua string) EditorInfo {
	tokens := strings.Split(ua, " ")
	nth := func(i int) *string {
		if i >= 0 && i < len(tokens) {
			v := tokens[i]
			return &v
		}
		return nil
	}
	return EditorInfo{
		Platform: nth(1),
		Editor:   nth(3),
		Plugin:   nth(4),
	}
}

// LanguageFromEntity derives a language name from a file entity's extension,
// reproducing addMissingLang in Heartbeats.hs. Returns nil when no language
// can be determined.
func LanguageFromEntity(entity string) *string {
	ext := filepath.Ext(entity) // includes leading dot, e.g. ".go"
	if ext == "" || ext == "." {
		return nil
	}
	// Drop the leading dot to match Haskell's splitExtension semantics.
	e := strings.TrimPrefix(ext, ".")
	var lang string
	switch e {
	case "":
		return nil
	case "org":
		lang = "Org"
	case "jinja", "jinja2":
		lang = "Jinja"
	case "tfvars":
		lang = "Terraform"
	case "cabal":
		lang = "Cabal Config"
	case "gotmpl":
		lang = "Go template"
	case "zig":
		lang = "Zig"
	case "purs":
		lang = "PureScript"
	case "dhall":
		lang = "Dhall"
	default:
		lang = strings.ToUpper(e)
	}
	return &lang
}
