package a

// Count is a named domain type.
type Count int

// withBare takes a bare primitive parameter and must be flagged.
func withBare(a int) {} // want `bare primitive`

// withNamed takes a named domain type and must not be flagged.
func withNamed(c Count) {}

// withSlice takes a composite (non-identifier) type; deferred in v1, not flagged.
func withSlice(s []string) {}

// noParams has no parameters and must not be flagged.
func noParams() {}

// T carries a method below.
type T struct{}

// method has a receiver; methods are deferred in v1 and must not be flagged.
func (T) method(a int) {}
