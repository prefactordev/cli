// Package skills holds the skills for coding agents that ship inside the
// CLI, one directory per skill, so `prefactor skills install` writes the
// skills that match the installed CLI.
package skills

import "embed"

// FS holds every skill directory, such as
// bootstrap-existing-agent-with-prefactor-cli/SKILL.md, and this file.
//
//go:embed *
var FS embed.FS
