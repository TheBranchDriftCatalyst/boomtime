package jobs

import (
	"reflect"
	"testing"
)

func TestRegistryOffload(t *testing.T) {
	r := NewRegistry()
	r.SetOffload("label-image")
	r.SetOffload("avatar-render")
	got := r.OffloadKinds()
	want := []string{"avatar-render", "label-image"} // sorted
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("OffloadKinds() = %v, want %v", got, want)
	}
}

func TestDeriveKindFilter(t *testing.T) {
	offload := []string{"avatar-render", "label-image"}

	cases := []struct {
		name                     string
		role                     string
		envIn, envEx             []string
		wantInclude, wantExclude []string
	}{
		{
			// A dedicated worker claims ONLY the offload kinds, so scaling to zero
			// can't orphan a server-resident/scheduled kind (boom-caxl).
			name: "worker derives include=offload", role: "worker",
			wantInclude: offload, wantExclude: nil,
		},
		{
			// The always-on server runs everything EXCEPT the offload kinds.
			name: "server derives exclude=offload", role: "server",
			wantInclude: nil, wantExclude: offload,
		},
		{
			// A single-pod dev/all deployment claims everything (no filter).
			name: "all claims everything", role: "all",
			wantInclude: nil, wantExclude: nil,
		},
		{
			// An explicit include override wins over the derivation entirely.
			name: "env include overrides", role: "worker",
			envIn:       []string{"avatar-render"},
			wantInclude: []string{"avatar-render"}, wantExclude: nil,
		},
		{
			// An explicit exclude override wins even for a worker role.
			name: "env exclude overrides", role: "worker",
			envEx:       []string{"foo"},
			wantInclude: nil, wantExclude: []string{"foo"},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in, ex := DeriveKindFilter(c.role, offload, c.envIn, c.envEx)
			if !reflect.DeepEqual(in, c.wantInclude) {
				t.Errorf("include = %v, want %v", in, c.wantInclude)
			}
			if !reflect.DeepEqual(ex, c.wantExclude) {
				t.Errorf("exclude = %v, want %v", ex, c.wantExclude)
			}
		})
	}
}
