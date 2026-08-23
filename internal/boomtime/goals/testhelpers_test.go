// testhelpers_test.go — package-goals mirrors of the ginkgo test helpers
// that internal/db/harness_test.go defines for the db-package tests
// (boom-8tn phase 2b). Kept in `package goals_test` so they compose with
// testutil (which imports db) without creating an import cycle.
//
// The helpers are shims — the assertions and Describe/It bodies of the
// moved tests are byte-identical to the pre-move originals.
package goals_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/db"
	"github.com/TheBranchDriftCatalyst/boomtime/internal/shared/testutil"
)

// openTestDBG opens the isolated test DB (via testutil) so the goals
// package tests can seed rows without pulling internal/db's package-local
// harness (which would create an import cycle: goals imports db, and
// db's test harness would need goals).
func openTestDBG() *db.DB {
	return testutil.OpenDB(GinkgoTB())
}

// senderFixtureG is a slim mirror of internal/db.SenderFixtureG. Only
// exposes the ONE method the moved tests call (`Sender()`).
type senderFixtureG struct {
	name string
}

func (f *senderFixtureG) Sender() string { return f.name }

// mkSender mirrors internal/db.mkSender: a unique, time-suffixed name.
func mkSender(prefix string) string {
	return prefix + "_" + time.Now().Format("150405.000000000")
}

// insertFreshUser mirrors internal/db.insertFreshUser: seeds the users
// row a heartbeat/goal's owner FK requires.
func insertFreshUser(d *db.DB, ctx context.Context, username string) error {
	_, err := d.Pool.Exec(ctx,
		`INSERT INTO users (username, hashed_password, salt_used) VALUES ($1,'\x00','\x00') ON CONFLICT DO NOTHING`,
		username)
	return err
}

// newSenderG mirrors internal/db.newSenderG: mint a unique user, seed
// the row, register cleanup, return a fixture.
func newSenderG(d *db.DB, prefix string) *senderFixtureG {
	ctx := context.Background()
	name := mkSender(prefix)
	Expect(insertFreshUser(d, ctx, name)).To(Succeed())
	DeferCleanup(func() {
		_, _ = d.Pool.Exec(ctx, `DELETE FROM goals WHERE owner=$1`, name)
		_, _ = d.Pool.Exec(ctx, `DELETE FROM users WHERE username=$1`, name)
	})
	return &senderFixtureG{name: name}
}

// doJSONReqG mirrors handler_test.doJSONReqG so the moved handler tests
// keep byte-identical bodies.
func doJSONReqG(e http.Handler, method, target, token string, body any) *httptest.ResponseRecorder {
	var buf bytes.Buffer
	if body != nil {
		Expect(json.NewEncoder(&buf).Encode(body)).To(Succeed())
	}
	req := httptest.NewRequest(method, target, &buf)
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Basic "+token)
	}
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	return rec
}

// semanticJSONDiffG mirrors handler_test.semanticJSONDiffG — normalizes
// two JSON blobs and returns "" when equal, otherwise a diff summary.
func semanticJSONDiffG(a, b string) string {
	var av, bv any
	if err := json.Unmarshal([]byte(a), &av); err != nil {
		return "left is not valid JSON: " + err.Error()
	}
	if err := json.Unmarshal([]byte(b), &bv); err != nil {
		return "right is not valid JSON: " + err.Error()
	}
	an, _ := json.Marshal(av)
	bn, _ := json.Marshal(bv)
	if string(an) != string(bn) {
		return "normalized forms differ"
	}
	return ""
}
