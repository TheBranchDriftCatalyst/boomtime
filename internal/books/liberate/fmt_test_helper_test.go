package liberate

import "fmt"

// sprintf exists so the redaction tests can format a ContentKey through the
// real fmt machinery without each test importing fmt.
func sprintf(format string, a ...any) string { return fmt.Sprintf(format, a...) }
