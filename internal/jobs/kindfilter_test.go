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

// The liberation routing contract (boom-piig). Liberation was the case that
// exposed the gap — its kind was never marked offload, so ~1040 jobs all ran in
// the always-on server pod on a single node. These pin the two halves of the
// fix: the server must STOP claiming an offloaded kind, and the worker must
// claim exactly it.
func TestDeriveKindFilterOffloadRouting(t *testing.T) {
	// Mirrors the real set once books declares its own: two host-registered
	// kinds plus the books one.
	offload := []string{"avatar-render", "label-image", "books-liberate-book"}

	t.Run("server excludes every offloaded kind", func(t *testing.T) {
		include, exclude := DeriveKindFilter("server", offload, nil, nil)
		if len(include) != 0 {
			t.Errorf("server include = %v, want empty (it claims everything not excluded)", include)
		}
		if !contains(exclude, "books-liberate-book") {
			t.Errorf("server exclude = %v, want it to contain books-liberate-book — "+
				"otherwise the server keeps claiming liberation and the fan-out does nothing", exclude)
		}
	})

	t.Run("worker claims ONLY offloaded kinds", func(t *testing.T) {
		include, exclude := DeriveKindFilter("worker", offload, nil, nil)
		if !contains(include, "books-liberate-book") {
			t.Errorf("worker include = %v, want books-liberate-book", include)
		}
		if len(exclude) != 0 {
			t.Errorf("worker exclude = %v, want empty", exclude)
		}
		// The orphan guard: a scale-to-zero worker must never claim a
		// server-resident or scheduled kind, or it takes the job to the grave
		// when it scales down.
		if contains(include, "books-reading-monitor") {
			t.Error("worker would claim a non-offload kind — the orphan bug this derivation exists to prevent")
		}
	})

	t.Run("server and worker sets are disjoint — no double-claim", func(t *testing.T) {
		_, serverExclude := DeriveKindFilter("server", offload, nil, nil)
		workerInclude, _ := DeriveKindFilter("worker", offload, nil, nil)
		// Everything the worker claims must be something the server refuses.
		for _, k := range workerInclude {
			if !contains(serverExclude, k) {
				t.Errorf("kind %q is claimed by the worker but NOT excluded on the server — both would claim it", k)
			}
		}
	})

	t.Run("the sweep kind stays on the server", func(t *testing.T) {
		// books-liberate-sweep only enqueues rows; it is deliberately absent from
		// the offload set so it never needs a drain pod with the library mounted.
		_, serverExclude := DeriveKindFilter("server", offload, nil, nil)
		if contains(serverExclude, "books-liberate-sweep") {
			t.Error("the sweep kind was offloaded; it should run on the server")
		}
	})

	t.Run("env override still wins for both roles", func(t *testing.T) {
		// The operator escape hatch must survive the offload derivation — this is
		// what keda-scaledjob-jobs.yaml relies on to pin its kind list.
		include, exclude := DeriveKindFilter("server", offload, []string{"books-liberate-book"}, nil)
		if !contains(include, "books-liberate-book") || len(exclude) != 0 {
			t.Errorf("env include did not override on server: include=%v exclude=%v", include, exclude)
		}
	})
}

func contains(hay []string, needle string) bool {
	for _, h := range hay {
		if h == needle {
			return true
		}
	}
	return false
}
