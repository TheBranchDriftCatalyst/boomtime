// export_test.go — package-goals-only exports of unexported symbols
// consumed by the external test package (goals_test). The build system
// only compiles this file into the test binary, so nothing here leaks
// into non-test builds.
package goals

// ItoaFastForTest re-exports itoaFast for the branch-coverage It that
// pins its numeric contract against strconv.Itoa. Byte-identical to the
// pre-move assertion; only the accessor name changed to signal test-only.
func ItoaFastForTest(n int) string { return itoaFast(n) }
