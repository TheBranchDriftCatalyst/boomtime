# Ginkgo migration guide

Epic: `gaka-tst-ginkgo` — convert every `_test.go` file 1:1 from the
Go stdlib `testing` package to
[ginkgo v2](https://onsi.github.io/ginkgo/) +
[gomega](https://onsi.github.io/gomega/).

## Ground rules

1. **Parallel migration.** For each stdlib file `foo_test.go` we add a
   sibling `foo_ginkgo_test.go` that ports every case 1:1. Both files
   run under `go test ./<pkg>/...`. Only after every stdlib case has a
   verified ginkgo equivalent do we delete the stdlib file.
2. **1:1 coverage.** No case dropped, no case fabricated. If the
   stdlib file has 12 `TestXxx` functions, the ginkgo file has 12
   `It()` (or `DescribeTable` rows). The ginkgo file's godoc header
   lists which case was `TestXxx`.
3. **One suite entry per package.** Every package gets a
   `<pkg>_suite_test.go` with:

   ```go
   func Test<Pkg>Suite(t *testing.T) {
       RegisterFailHandler(Fail)
       RunSpecs(t, "internal/<pkg> suite")
   }
   ```

   This is the only `func Test*(t *testing.T)` remaining after the
   stdlib deletion. Everything else is `Describe`/`Context`/`It`.
4. **Same package.** `_ginkgo_test.go` lives inside the target
   package (`package labels`, not `package labels_test`) so it can
   test unexported helpers the stdlib file already tests. `_test.go`
   files that use the `_test` suffix (`handler_test`) keep that too.

## Assertion mapping

| stdlib                                              | ginkgo                                                    |
| --------------------------------------------------- | --------------------------------------------------------- |
| `if got != want { t.Errorf(...) }`                  | `Expect(got).To(Equal(want))`                             |
| `if err != nil { t.Fatalf(...) }`                   | `Expect(err).NotTo(HaveOccurred())`                       |
| `if err == nil { t.Error(...) }`                    | `Expect(err).To(HaveOccurred())`                          |
| `if !ok { t.Error(...) }`                           | `Expect(cond).To(BeTrue())`                               |
| `if s == "" { t.Error(...) }`                       | `Expect(s).NotTo(BeEmpty())`                              |
| `if _, ok := x.(T); !ok { t.Errorf(...) }`          | `Expect(x).To(BeAssignableToTypeOf(T{}))`                 |
| `if len(xs) != 3 { t.Errorf(...) }`                 | `Expect(xs).To(HaveLen(3))`                               |
| `if got < want { t.Errorf(...) }`                   | `Expect(got).To(BeNumerically(">=", want))`               |
| `if !contains(xs, "a") { t.Error(...) }`            | `Expect(xs).To(ContainElement("a"))`                      |
| `for _, c := range cases { ... }` (table-driven)    | `DescribeTable(...)` + `Entry("case name", ...)`          |

## Setup / teardown

| stdlib                                              | ginkgo                                    |
| --------------------------------------------------- | ----------------------------------------- |
| Manual `t.Cleanup(...)`                             | `AfterEach(func() { ... })`               |
| Shared setup inline in each test                    | `BeforeEach(func() { ... })`              |
| `TestMain` for once-per-package setup               | `BeforeSuite(func() { ... })` in the suite file |
| One-time teardown                                   | `AfterSuite(...)`                         |

## Name collisions

Ginkgo v2 exports `Entry(...)` (for DescribeTable rows) as a top-level
symbol. Any package that defines a type or var named `Entry` (or
`Fail`, `It`, etc.) can't dot-import ginkgo — the compiler flags the
collision. In practice this hit `internal/labelcatalog` which declares
`type Entry struct{...}`.

Workaround: named import ginkgo (not gomega — gomega's `Expect`,
`Equal`, `BeNil` rarely collide):

```go
import (
    "github.com/onsi/ginkgo/v2"      // no dot import
    . "github.com/onsi/gomega"
)

var _ = ginkgo.Describe("...", func() {
    ginkgo.It("...", func() {
        Expect(x).To(Equal(y))       // gomega dot-import stays
    })
})
```

The suite entry file also needs the named import
(`ginkgo.Fail`, `ginkgo.RunSpecs`).

## Handling `*testing.T`

Legacy helpers still typed `*testing.T` (e.g. `testutil.NewHarness(t)`)
accept `ginkgo.GinkgoT()` — it implements the `TestingT` interface.
When migrating, wrap: `testutil.NewHarness(GinkgoT())`.

## Table-driven → DescribeTable

A stdlib table-driven test:

```go
for _, c := range cases {
    got := f(c.in)
    if got != c.want {
        t.Errorf("f(%q) = %q, want %q", c.in, got, c.want)
    }
}
```

becomes:

```go
DescribeTable("f",
    func(in, want string) {
        Expect(f(in)).To(Equal(want))
    },
    Entry("basic case",    "abc", "cba"),
    Entry("empty",         "",    ""),
    Entry("special chars", "!@#", "#@!"),
)
```

Each `Entry` shows as its own line in `go test -v` output. Named
entries beat the stdlib "cases[3]" pointer chase.

## `go test` output

Stdlib:

```
=== RUN   TestSpecFromDBRow_TierRowGetsTierKey
--- PASS: TestSpecFromDBRow_TierRowGetsTierKey (0.00s)
```

Ginkgo:

```
Running Suite: internal/labels suite
Will run 9 of 9 specs
•••••••••
Ran 9 of 9 Specs in 0.000 seconds
SUCCESS! — 9 Passed | 0 Failed
```

Use `-v` for the nested spec tree. Use `-ginkgo.focus="regex"` to filter.

## Kill switch

After every stdlib case has a verified ginkgo equivalent in every
package (the last child of `gaka-tst-ginkgo`):

1. Delete every stdlib `*_test.go` **except** each package's
   `<pkg>_suite_test.go`.
2. Rename every `*_ginkgo_test.go` → `*_test.go`.
3. Confirm `go test ./...` is green with only ginkgo specs.
4. Update this doc to remove the "parallel" section.

## Per-package migration ticket template

Each package's migration ticket links to this doc and lists:

- The stdlib test files to convert
- The number of `TestXxx` functions to preserve (proof of 1:1)
- The suite file to create (`<pkg>_suite_test.go`)
- Dependencies (other packages migrated first)
- Verification: `go test ./<pkg>/...` runs both suites cleanly

The last child of the epic is the kill switch — deletes stdlib files
after ALL package migrations are verified.
