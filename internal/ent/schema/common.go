package schema

import "time"

// now is a seam so tests can override creation timestamps if needed.
// Not exported — only used as a default-func target.
func now() time.Time { return time.Now() }
