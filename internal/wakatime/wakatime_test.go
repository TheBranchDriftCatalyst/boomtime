package wakatime

import "testing"

func TestUserAgentInfo(t *testing.T) {
	// tokens: [0]=wakatime/1.0 [1]=(Linux-5.4) [2]=go1.20 [3]=vscode/1.70 [4]=vscode-wakatime/4.0
	ua := "wakatime/1.0 (Linux-5.4) go1.20 vscode/1.70 vscode-wakatime/4.0"
	info := UserAgentInfo(ua)
	if info.Platform == nil || *info.Platform != "(Linux-5.4)" {
		t.Fatalf("platform = %v, want (Linux-5.4)", info.Platform)
	}
	if info.Editor == nil || *info.Editor != "vscode/1.70" {
		t.Fatalf("editor = %v, want vscode/1.70", info.Editor)
	}
	if info.Plugin == nil || *info.Plugin != "vscode-wakatime/4.0" {
		t.Fatalf("plugin = %v, want vscode-wakatime/4.0", info.Plugin)
	}
}

func TestUserAgentInfoShort(t *testing.T) {
	info := UserAgentInfo("only two")
	if info.Platform == nil || *info.Platform != "two" {
		t.Fatalf("platform = %v, want two", info.Platform)
	}
	if info.Editor != nil {
		t.Fatalf("editor = %v, want nil", info.Editor)
	}
	if info.Plugin != nil {
		t.Fatalf("plugin = %v, want nil", info.Plugin)
	}
}

func TestLanguageFromEntity(t *testing.T) {
	cases := []struct {
		entity string
		want   *string
	}{
		{"main.go", strptr("GO")},
		{"main.zig", strptr("Zig")},
		{"vars.tfvars", strptr("Terraform")},
		{"notes.org", strptr("Org")},
		{"template.jinja2", strptr("Jinja")},
		{"noext", nil},
		{"trailingdot.", nil},
	}
	for _, c := range cases {
		got := LanguageFromEntity(c.entity)
		if (got == nil) != (c.want == nil) {
			t.Fatalf("LanguageFromEntity(%q) = %v, want %v", c.entity, got, c.want)
		}
		if got != nil && *got != *c.want {
			t.Fatalf("LanguageFromEntity(%q) = %q, want %q", c.entity, *got, *c.want)
		}
	}
}

func strptr(s string) *string { return &s }
