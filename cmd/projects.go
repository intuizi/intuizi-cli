package cmd

import (
	"github.com/spf13/cobra"
)

const projectsPrefix = "/analyses/projects"

var projectColumns = []string{"id", "name"}

var projectsCmd = &cobra.Command{
	Use:   "projects",
	Short: "Manage projects",
	Long: `Manage projects.

A project is a folder. Audiences, activations, cohorts and schedules can each
carry a project_id, and the console groups them by it.

Deleting a project does not delete what was filed under it.`,
}

func projectsListCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List projects",
		Args:  cobra.NoArgs,
	}
	query := listFlags(cmd, "Case-insensitive contains match on the project name")

	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		return renderList(cmd, projectsPrefix+"/index", query(), projectColumns, "no projects")
	}
	return cmd
}

func projectsCreateCommand() *cobra.Command {
	var name string

	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a project",
		Long: `Create a project.

The whole body is a name, so this one takes a flag rather than a file:

    intuizi projects create --name "Retail 2026"`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return createBody(cmd, projectsPrefix+"/create",
				map[string]any{"name": name}, projectColumns, "")
		},
	}

	cmd.Flags().StringVar(&name, "name", "", "Project name (max 255 characters)")
	_ = cmd.MarkFlagRequired("name")
	return cmd
}

func init() {
	projectsCmd.AddCommand(
		projectsListCommand(),
		showCommand("project", projectsPrefix, projectColumns),
		deleteCommand("project", projectsPrefix),
		projectsCreateCommand(),
	)
	rootCmd.AddCommand(projectsCmd)
}
