// sources_health_ginkgo_test.go — ginkgo mirror of sources_health_test.go (gaka-0vp.13).
// 1:1 case map (1 stdlib TestXxx → 1 It):
//   TestSourceHealthShape → "ListSourceHealth > per-(plugin,machine) shape"
package db

import (
	"context"
	"time"

	"github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = ginkgo.Describe("ListSourceHealth", func() {
	ginkgo.It("returns per-(plugin,machine) MAX(time_sent)+count, excludes plugin-less, folds NULL machine to 'unknown', orders stalest-first", func() {
		d := openTestDBG()
		ctx := context.Background()

		sender := "srchealth_user_" + time.Now().Format("150405.000000")
		_, err := d.Pool.Exec(ctx, `INSERT INTO users (username, hashed_password, salt_used) VALUES ($1,'\x00','\x00') ON CONFLICT DO NOTHING`, sender)
		Expect(err).NotTo(HaveOccurred())
		_, err = d.Pool.Exec(ctx, `INSERT INTO projects (owner, name) VALUES ($1,'proj') ON CONFLICT DO NOTHING`, sender)
		Expect(err).NotTo(HaveOccurred())
		ginkgo.DeferCleanup(func() {
			_, _ = d.Pool.Exec(ctx, `DELETE FROM heartbeats WHERE sender=$1`, sender)
			_, _ = d.Pool.Exec(ctx, `DELETE FROM projects WHERE owner=$1`, sender)
			_, _ = d.Pool.Exec(ctx, `DELETE FROM users WHERE username=$1`, sender)
		})

		// The heartbeats unique constraint is (entity, sender, time_sent); give each
		// row a distinct entity so no two collide on the same timestamp.
		insert := func(entity string, ts time.Time, plugin, machine *string) {
			_, err := d.Pool.Exec(ctx,
				`INSERT INTO heartbeats (sender, project, entity, ty, time_sent, user_agent, plugin, machine)
				 VALUES ($1,'proj',$2,'file',$3,'ua',$4,$5)`,
				sender, entity, ts, plugin, machine)
			Expect(err).NotTo(HaveOccurred())
		}

		recent := time.Now().UTC().Add(-2 * time.Hour)
		old := time.Now().UTC().Add(-40 * 24 * time.Hour)
		vscodePlugin := "vscode-wakatime"
		vimPlugin := "vim-wakatime"
		laptop := "laptop"
		desktop := "desktop"

		// Same plugin on two machines => two distinct sources (compound key).
		insert("a.go", recent, &vscodePlugin, &laptop)
		insert("b.go", recent.Add(-time.Hour), &vscodePlugin, &laptop) // second beat (count=2)
		insert("c.go", old, &vscodePlugin, &desktop)                   // vscode on desktop: stale (oldest)
		insert("d.go", recent, &vimPlugin, &laptop)                    // different plugin, same machine
		insert("e.go", recent, nil, &laptop)                           // NULL plugin -> excluded
		insert("f.go", recent, &vimPlugin, nil)                        // NULL machine -> 'unknown'

		got, err := d.ListSourceHealth(ctx, sender)
		Expect(err).NotTo(HaveOccurred())

		byKey := map[string]SourceHealth{}
		for _, s := range got {
			Expect(s.Plugin).NotTo(BeEmpty(), "plugin-less heartbeat leaked into results: %+v", s)
			byKey[s.Plugin+"@"+s.Machine] = s
		}

		// vscode-wakatime @ laptop: two beats, most recent.
		v, ok := byKey["vscode-wakatime@laptop"]
		Expect(ok).To(BeTrue())
		Expect(v.Count).To(BeEquivalentTo(2))
		Expect(v.LastSeen.Equal(recent)).To(BeTrue(), "vscode-wakatime@laptop lastSeen = %v, want %v", v.LastSeen, recent)

		// Same plugin on a different machine is a separate source.
		_, ok = byKey["vscode-wakatime@desktop"]
		Expect(ok).To(BeTrue(), "missing vscode-wakatime@desktop source; got %+v", got)

		// Different plugin on the same machine is a separate source.
		_, ok = byKey["vim-wakatime@laptop"]
		Expect(ok).To(BeTrue(), "missing vim-wakatime@laptop source; got %+v", got)

		// Missing machine collapses to 'unknown'.
		_, ok = byKey["vim-wakatime@unknown"]
		Expect(ok).To(BeTrue(), "missing vim-wakatime@unknown source; got %+v", got)

		// Stalest-first: results are ordered by lastSeen ASC.
		for i := 1; i < len(got); i++ {
			Expect(got[i].LastSeen.Before(got[i-1].LastSeen)).To(BeFalse(),
				"results not ordered stalest-first: [%d]=%v before [%d]=%v",
				i, got[i].LastSeen, i-1, got[i-1].LastSeen)
		}
	})
})
