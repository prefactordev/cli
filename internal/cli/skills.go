package cli

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/prefactordev/cli/skills"
)

// skillAgent is a coding agent that reads skills, with the IDs and
// directories the skills CLI (github.com/vercel-labs/skills) uses.
type skillAgent struct {
	id, name string
	// project is the skills directory in a project; global is the one in
	// the home directory.
	project, global string
	// detect is the home directory entry that shows the agent is
	// installed, or empty when only --agent selects it.
	detect string
}

var skillAgents = []skillAgent{
	{"claude-code", "Claude Code", ".claude/skills", ".claude/skills", ".claude"},
	{"codex", "Codex", ".agents/skills", ".agents/skills", ".codex"},
	{"cursor", "Cursor", ".agents/skills", ".cursor/skills", ".cursor"},
	{"opencode", "OpenCode", ".agents/skills", ".config/opencode/skills", ".config/opencode"},
	{"github-copilot", "GitHub Copilot", ".agents/skills", ".copilot/skills", ".copilot"},
	{"gemini-cli", "Gemini CLI", ".agents/skills", ".gemini/skills", ".gemini"},
	{"windsurf", "Windsurf", ".windsurf/skills", ".codeium/windsurf/skills", ".codeium/windsurf"},
	{"amp", "Amp", ".agents/skills", ".config/agents/skills", ".config/amp"},
	{"universal", "Other agents", ".agents/skills", ".config/agents/skills", ""},
}

func skillAgentIDs() []string {
	ids := make([]string, len(skillAgents))
	for i, a := range skillAgents {
		ids[i] = a.id
	}
	return ids
}

// skillInstall is where one directory of skills went, and for which agents.
type skillInstall struct {
	Path   string   `json:"path"`
	Agents []string `json:"agents"`
}

func newSkillsCommand(app *App) *cobra.Command {
	group := &cobra.Command{
		Use:   "skills",
		Short: "Install skills that teach coding agents to use the CLI",
	}
	install := &cobra.Command{
		Use:   "install",
		Short: "Install the CLI's skills for the coding agents on this machine",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			global, _ := cmd.Flags().GetBool("global")
			ids, _ := cmd.Flags().GetStringSlice("agent")
			installs, names, err := installSkills(ids, global)
			if err != nil {
				return err
			}
			if app.jsonOutput() {
				return app.printJSONValue(cmd, map[string]any{"skills": names, "installs": installs})
			}
			out := cmd.OutOrStdout()
			for _, in := range installs {
				if _, err := fmt.Fprintf(out, "Installed %s for %s in %s\n", strings.Join(names, ", "), strings.Join(in.Agents, ", "), in.Path); err != nil {
					return err
				}
			}
			return nil
		},
	}
	install.Flags().Bool("global", false, "Install into the home directory instead of the current project")
	install.Flags().StringSlice("agent", nil, "Coding agent to install for, repeatable (choices: "+strings.Join(skillAgentIDs(), ", ")+"; default: the agents found in the home directory)")
	// An empty list is not worth showing as the default.
	install.Flags().Lookup("agent").DefValue = ""
	group.AddCommand(install)
	return group
}

// installSkills writes every bundled skill into the skills directory of
// each agent, replacing an earlier copy, and returns where they went and
// the skills' names. Agents that share a directory get one copy.
func installSkills(ids []string, global bool) ([]skillInstall, []string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, nil, err
	}
	agents, err := chooseSkillAgents(ids, home)
	if err != nil {
		return nil, nil, err
	}
	base := home
	if !global {
		if base, err = os.Getwd(); err != nil {
			return nil, nil, err
		}
	}

	names, err := bundledSkills()
	if err != nil {
		return nil, nil, err
	}
	var installs []skillInstall
	for _, a := range agents {
		dir := a.project
		if global {
			dir = a.global
		}
		path := filepath.Join(base, filepath.FromSlash(dir))
		i := slices.IndexFunc(installs, func(in skillInstall) bool { return in.Path == path })
		if i >= 0 {
			installs[i].Agents = append(installs[i].Agents, a.name)
			continue
		}
		for _, name := range names {
			if err := writeSkill(name, filepath.Join(path, name)); err != nil {
				return nil, nil, err
			}
		}
		installs = append(installs, skillInstall{Path: path, Agents: []string{a.name}})
	}
	return installs, names, nil
}

// chooseSkillAgents returns the agents named by ids, or the agents found
// in the home directory when there are none.
func chooseSkillAgents(ids []string, home string) ([]skillAgent, error) {
	var agents []skillAgent
	for _, id := range ids {
		i := slices.IndexFunc(skillAgents, func(a skillAgent) bool { return a.id == id })
		if i < 0 {
			return nil, fmt.Errorf("--agent must be one of: %s.", strings.Join(skillAgentIDs(), ", "))
		}
		if !slices.ContainsFunc(agents, func(a skillAgent) bool { return a.id == id }) {
			agents = append(agents, skillAgents[i])
		}
	}
	if len(ids) > 0 {
		return agents, nil
	}
	for _, a := range skillAgents {
		if a.detect == "" {
			continue
		}
		if _, err := os.Stat(filepath.Join(home, filepath.FromSlash(a.detect))); err == nil {
			agents = append(agents, a)
		}
	}
	if len(agents) == 0 {
		return nil, fmt.Errorf("No coding agent found in %s. Pass --agent with one of: %s.", home, strings.Join(skillAgentIDs(), ", "))
	}
	return agents, nil
}

// bundledSkills lists the skills built into the CLI.
func bundledSkills() ([]string, error) {
	entries, err := fs.ReadDir(skills.FS, ".")
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	if len(names) == 0 {
		return nil, errors.New("this CLI was built without skills")
	}
	return names, nil
}

// writeSkill replaces dest with the bundled skill name.
func writeSkill(name, dest string) error {
	if err := os.RemoveAll(dest); err != nil {
		return err
	}
	sub, err := fs.Sub(skills.FS, name)
	if err != nil {
		return err
	}
	return os.CopyFS(dest, sub)
}
