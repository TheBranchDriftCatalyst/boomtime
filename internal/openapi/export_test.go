package openapi

// Test seams — visible ONLY under `go test` because export_test.go is
// compiled into the test binary but not the production binary. Keeps
// strPtr/itoa unexported for production callers while allowing the
// external test package to hit them directly.

var (
	StrPtrForTest = strPtr
	ItoaForTest   = itoa
)
