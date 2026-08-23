// widgets_ginkgo_test.go — ginkgo mirror of widgets_test.go (boom-0vp.13).
// 1:1 case map (2 stdlib TestXxx → 2 Its):
//
//	TestExactSourcesFor                       → "ExactSourcesFor > reverse-lookup"
//	TestProjectMemberSetWithRenamesExpands    → "ProjectMemberSetWithRenames > expands renames into scope"
package db

import (
	"sort"

	"github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = ginkgo.Describe("widgets curation helpers", func() {
	ginkgo.It("ExactSourcesFor returns every raw source name that renames to the target", func() {
		rs := mkRenames("project", map[string]string{
			"hakatime":  "boomtime",
			"boomtime":  "boomtime", // idempotent (identity rename)
			"catalyst":  "boomtime", // merged into boomtime too
			"unrelated": "other",
		})

		got := rs.ExactSourcesFor("project", "boomtime")
		sort.Strings(got)
		Expect(got).To(Equal([]string{"boomtime", "catalyst", "hakatime"}))

		// Target that no rule maps to → nil (widget mint 404 stays 404).
		Expect(rs.ExactSourcesFor("project", "no-such-target")).To(BeNil())

		// Wrong axis → nil (mint 404 stays 404).
		Expect(rs.ExactSourcesFor("language", "boomtime")).To(BeNil())
	})

	ginkgo.It("ProjectMemberSetWithRenames expands renames into scope", func() {
		rs := mkRenames("project", map[string]string{
			"hakatime": "boomtime",
			"catalyst": "boomtime",
		})

		ms := ProjectMemberSetWithRenames("boomtime", rs)
		got := ms.byAxis["project"].exact
		sort.Strings(got)
		Expect(got).To(Equal([]string{"boomtime", "catalyst", "hakatime"}))

		// No renames touching this axis → member set is exactly the scope-ref.
		empty := ProjectMemberSetWithRenames("boomtime", RenameSets{})
		Expect(empty.byAxis["project"].exact).To(Equal([]string{"boomtime"}))

		// The scope-ref itself doesn't appear as a rename source but IS the
		// target → the source list dedupes it so we don't send ["b","b"].
		self := mkRenames("project", map[string]string{
			"boomtime": "boomtime",
		})
		Expect(ProjectMemberSetWithRenames("boomtime", self).byAxis["project"].exact).To(Equal([]string{"boomtime"}))
	})
})

// -- helpers restored from stdlib partner (boom-0vp.17) --
func mkRenames(axis string, exact map[string]string) RenameSets {
	rs := RenameSets{byAxis: map[string]axisRenames{}}
	a := rs.byAxis[axis]
	if a.exact == nil {
		a.exact = map[string]string{}
	}
	for src, tgt := range exact {
		a.exact[src] = tgt
	}
	rs.byAxis[axis] = a
	return rs
}
