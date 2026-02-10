package instructions

import "fmt"

// BuildEnvironmentContext produces an XML-formatted environment context
// string, following the Codex pattern for injecting context as a user message
// at session start.
func BuildEnvironmentContext(cwd, shell string) string {
	if shell == "" {
		shell = "bash"
	}

	return fmt.Sprintf(`<environment_context>
  <cwd>%s</cwd>
  <shell>%s</shell>
</environment_context>`, cwd, shell)
}
