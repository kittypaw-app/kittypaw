package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/jinto/kittypaw/store"
)

type projectSharedFlags struct {
	accountID string
}

type projectCreateFlags struct {
	shared   *projectSharedFlags
	rootPath string
	name     string
}

type projectMilestoneCreateFlags struct {
	shared      *projectSharedFlags
	targetDate  string
	description string
}

func newProjectCmd() *cobra.Command {
	flags := &projectSharedFlags{}
	cmd := &cobra.Command{
		Use:   "project",
		Short: "Manage local Kanban projects",
	}
	cmd.PersistentFlags().StringVar(&flags.accountID, "account", "", "local account id")
	cmd.AddCommand(
		newProjectCreateCmd(flags),
		newProjectListCmd(flags),
		newProjectShowCmd(flags),
		newProjectBoardCmd(flags),
		newProjectMilestoneCmd(flags),
	)
	return cmd
}

func newProjectCreateCmd(shared *projectSharedFlags) *cobra.Command {
	flags := &projectCreateFlags{shared: shared}
	cmd := &cobra.Command{
		Use:   "create <slug>",
		Short: "Create a local Kanban project",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runProjectCreate(args[0], flags)
		},
	}
	cmd.Flags().StringVar(&flags.rootPath, "root", "", "project root path")
	cmd.Flags().StringVar(&flags.name, "name", "", "project display name")
	return cmd
}

func newProjectListCmd(shared *projectSharedFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List local Kanban projects",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runProjectList(shared)
		},
	}
	return cmd
}

func newProjectShowCmd(shared *projectSharedFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "show <project>",
		Short: "Show a local Kanban project",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runProjectShow(args[0], shared)
		},
	}
	return cmd
}

func newProjectBoardCmd(shared *projectSharedFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "board",
		Short: "Manage project boards",
	}
	cmd.AddCommand(newProjectBoardListCmd(shared))
	return cmd
}

func newProjectBoardListCmd(shared *projectSharedFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list <project>",
		Short: "List project boards",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runProjectBoardList(args[0], shared)
		},
	}
	return cmd
}

func newProjectMilestoneCmd(shared *projectSharedFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "milestone",
		Short: "Manage project milestones",
	}
	cmd.AddCommand(
		newProjectMilestoneCreateCmd(shared),
		newProjectMilestoneListCmd(shared),
	)
	return cmd
}

func newProjectMilestoneCreateCmd(shared *projectSharedFlags) *cobra.Command {
	flags := &projectMilestoneCreateFlags{shared: shared}
	cmd := &cobra.Command{
		Use:   "create <project> <title>",
		Short: "Create a project milestone",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runProjectMilestoneCreate(args[0], args[1], flags)
		},
	}
	cmd.Flags().StringVar(&flags.targetDate, "target-date", "", "target date in YYYY-MM-DD format")
	cmd.Flags().StringVar(&flags.description, "description", "", "milestone description")
	return cmd
}

func newProjectMilestoneListCmd(shared *projectSharedFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list <project>",
		Short: "List project milestones",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runProjectMilestoneList(args[0], shared)
		},
	}
	return cmd
}

func runProjectCreate(slug string, flags *projectCreateFlags) error {
	if flags == nil {
		flags = &projectCreateFlags{shared: &projectSharedFlags{}}
	}
	rootPath, err := normalizeProjectRoot(flags.rootPath)
	if err != nil {
		return err
	}
	st, err := openKanbanCommandStore(flags.shared.accountID)
	if err != nil {
		return err
	}
	defer st.Close()

	project, err := st.CreateKanbanProject(store.CreateKanbanProjectRequest{
		Slug:     slug,
		Name:     flags.name,
		RootPath: rootPath,
	})
	if err != nil {
		return err
	}
	board, err := st.GetDefaultKanbanBoard(project.ID)
	if err != nil {
		return fmt.Errorf("load default board: %w", err)
	}
	fmt.Printf("Created project: %s\n", project.Slug)
	fmt.Printf("ID: %s\n", project.ID)
	fmt.Printf("Root: %s\n", project.RootPath)
	fmt.Printf("Default board: %s\n", board.Slug)
	return nil
}

func runProjectList(flags *projectSharedFlags) error {
	st, err := openKanbanCommandStore(projectAccountID(flags))
	if err != nil {
		return err
	}
	defer st.Close()

	projects, err := st.ListKanbanProjects(false)
	if err != nil {
		return err
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "Slug\tName\tRoot")
	for _, project := range projects {
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\n", project.Slug, project.Name, project.RootPath)
	}
	return w.Flush()
}

func runProjectShow(projectArg string, flags *projectSharedFlags) error {
	st, err := openKanbanCommandStore(projectAccountID(flags))
	if err != nil {
		return err
	}
	defer st.Close()

	project, err := resolveKanbanProject(st, projectArg)
	if err != nil {
		return err
	}
	fmt.Printf("Project: %s\n", project.Slug)
	fmt.Printf("ID: %s\n", project.ID)
	fmt.Printf("Name: %s\n", project.Name)
	fmt.Printf("Root: %s\n", project.RootPath)
	fmt.Printf("Archived: %t\n", project.Archived)
	return nil
}

func runProjectBoardList(projectArg string, flags *projectSharedFlags) error {
	st, err := openKanbanCommandStore(projectAccountID(flags))
	if err != nil {
		return err
	}
	defer st.Close()

	project, err := resolveKanbanProject(st, projectArg)
	if err != nil {
		return err
	}
	boards, err := st.ListKanbanBoards(project.ID)
	if err != nil {
		return err
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "Slug\tName\tDefault")
	for _, board := range boards {
		def := ""
		if board.IsDefault {
			def = "yes"
		}
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\n", board.Slug, board.Name, def)
	}
	return w.Flush()
}

func runProjectMilestoneCreate(projectArg, title string, flags *projectMilestoneCreateFlags) error {
	if flags == nil {
		flags = &projectMilestoneCreateFlags{shared: &projectSharedFlags{}}
	}
	targetDate, err := normalizeKanbanDate("target-date", flags.targetDate)
	if err != nil {
		return err
	}
	st, err := openKanbanCommandStore(flags.shared.accountID)
	if err != nil {
		return err
	}
	defer st.Close()

	project, err := resolveKanbanProject(st, projectArg)
	if err != nil {
		return err
	}
	milestone, err := st.CreateKanbanMilestone(store.CreateKanbanMilestoneRequest{
		ProjectID:   project.ID,
		Title:       title,
		Description: strings.TrimSpace(flags.description),
		TargetDate:  targetDate,
	})
	if err != nil {
		return err
	}
	fmt.Printf("Created milestone: %s\n", milestone.Slug)
	fmt.Printf("ID: %s\n", milestone.ID)
	fmt.Printf("Project: %s\n", project.Slug)
	if milestone.TargetDate != "" {
		fmt.Printf("Target date: %s\n", milestone.TargetDate)
	}
	return nil
}

func runProjectMilestoneList(projectArg string, flags *projectSharedFlags) error {
	st, err := openKanbanCommandStore(projectAccountID(flags))
	if err != nil {
		return err
	}
	defer st.Close()

	project, err := resolveKanbanProject(st, projectArg)
	if err != nil {
		return err
	}
	milestones, err := st.ListKanbanMilestones(project.ID)
	if err != nil {
		return err
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "Slug\tTitle\tStatus\tTarget")
	for _, milestone := range milestones {
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", milestone.Slug, milestone.Title, milestone.Status, milestone.TargetDate)
	}
	return w.Flush()
}

func openKanbanCommandStore(accountID string) (*store.Store, error) {
	resolved, err := resolveCLIAccountWithContext(accountID)
	if err != nil {
		return nil, err
	}
	return openStoreForAccount(resolved)
}

func projectAccountID(flags *projectSharedFlags) string {
	if flags == nil {
		return ""
	}
	return flags.accountID
}

func resolveKanbanProject(st *store.Store, projectArg string) (*store.KanbanProject, error) {
	projectArg = strings.TrimSpace(projectArg)
	if projectArg == "" {
		return nil, fmt.Errorf("project is required")
	}
	project, err := st.GetKanbanProject(projectArg)
	if err != nil {
		return nil, fmt.Errorf("project %q: %w", projectArg, err)
	}
	return project, nil
}

func normalizeProjectRoot(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", fmt.Errorf("--root is required")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve --root: %w", err)
	}
	return abs, nil
}

func normalizeKanbanDate(flagName, value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	if _, err := time.Parse("2006-01-02", value); err != nil {
		return "", fmt.Errorf("--%s must use YYYY-MM-DD", flagName)
	}
	return value, nil
}
