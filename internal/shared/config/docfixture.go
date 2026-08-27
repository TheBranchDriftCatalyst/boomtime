package config

// DocumentationFixture returns a Config with every ROUTE-GATING predicate
// satisfied, for building the documentation router that the OpenAPI spec is
// generated from.
//
// NEVER SERVE TRAFFIC WITH THIS. It is an enumeration fixture: it claims
// credentials it does not have and a library path that is not mounted. It exists
// so route registration takes every branch, and for nothing else.
//
// WHY IT EXISTS. Routes are gated by plain `if` blocks inside each domain's
// Register func, so the set of registered routes is a function of config. The
// spec's router walk and the drift guard were both built against a zero-value
// handler, which makes every predicate false — and that is why the entire books
// domain (~26 routes), GitHub connect, the admin CLI, and the jobs/metrics
// cluster were silently absent from the spec. A gated route could never drift,
// because it was never seen. (boom-i18f)
//
// KEEP THIS IN LOCKSTEP WITH THE PREDICATES, not with the flags. Several are
// COMPOUND — LiberationEnabled() also requires BooksLibraryPath, and
// GithubConnectEnabled() also requires all three OAuth values — so setting the
// feature bool alone leaves the routes hidden and the fixture silently useless.
// The docrouter test asserts a floor on the route count to catch exactly that.
func DocumentationFixture() *Config {
	return &Config{
		// BooksEnabled() — master gate for the catalyst-books surface.
		FeatureBooks: true,
		// LiberationEnabled() = FeatureBooks && FeatureBooksLiberation && path.
		FeatureBooksLiberation: true,
		BooksLibraryPath:       "/documentation-fixture/not-a-real-library",
		// GithubConnectEnabled() folds the credentials into the predicate, so
		// the flag alone is not enough to register the OAuth routes.
		FeatureGithubStats:      true,
		GithubOAuthClientID:     "documentation-fixture",
		GithubOAuthClientSecret: "documentation-fixture",
		OAuthStateSigningKey:    "documentation-fixture",
		// FeatureAdminCLI is a bare bool gate on the CLI-runner routes.
		FeatureAdminCLI: true,
	}
}
