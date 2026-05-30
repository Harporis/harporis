package cmd

// exitError is the typed error consumed by Execute() in root.go to set
// the process exit code. Concrete codes are documented in the CLI README
// and the spec.
type exitError struct {
	code int
	msg  string
}

func (e *exitError) Error() string { return e.msg }
func (e *exitError) ExitCode() int { return e.code }
