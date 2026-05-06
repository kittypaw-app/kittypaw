package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/jinto/kittypaw/store"
)

type kanbanSharedFlags struct {
	accountID string
}

type kanbanCreateFlags struct {
	shared    *kanbanSharedFlags
	project   string
	board     string
	milestone string
	body      string
	assignee  string
	createdBy string
	priority  int
	status    string
}

type kanbanListFlags struct {
	shared    *kanbanSharedFlags
	project   string
	board     string
	milestone string
	status    string
}

type kanbanClaimFlags struct {
	shared  *kanbanSharedFlags
	actor   string
	workDir string
}

type kanbanCompleteFlags struct {
	shared   *kanbanSharedFlags
	actor    string
	summary  string
	metadata string
}

type kanbanBlockFlags struct {
	shared *kanbanSharedFlags
	actor  string
}

type kanbanUnblockFlags struct {
	shared  *kanbanSharedFlags
	actor   string
	comment string
}

type kanbanCommentFlags struct {
	shared *kanbanSharedFlags
	author string
}

func newKanbanCmd() *cobra.Command {
	flags := &kanbanSharedFlags{}
	cmd := &cobra.Command{
		Use:   "kanban",
		Short: "Manage local Kanban tasks",
	}
	cmd.PersistentFlags().StringVar(&flags.accountID, "account", "", "local account id")
	cmd.AddCommand(
		newKanbanCreateCmd(flags),
		newKanbanListCmd(flags),
		newKanbanShowCmd(flags),
		newKanbanClaimCmd(flags),
		newKanbanCompleteCmd(flags),
		newKanbanBlockCmd(flags),
		newKanbanUnblockCmd(flags),
		newKanbanCommentCmd(flags),
		newKanbanLinkCmd(flags),
		newKanbanRunsCmd(flags),
	)
	return cmd
}

func newKanbanCreateCmd(shared *kanbanSharedFlags) *cobra.Command {
	flags := &kanbanCreateFlags{shared: shared}
	cmd := &cobra.Command{
		Use:   "create <title>",
		Short: "Create a Kanban task",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runKanbanCreate(args[0], flags)
		},
	}
	cmd.Flags().StringVar(&flags.project, "project", "", "project id or slug")
	cmd.Flags().StringVar(&flags.board, "board", "", "board id or slug")
	cmd.Flags().StringVar(&flags.milestone, "milestone", "", "milestone id or slug")
	cmd.Flags().StringVar(&flags.body, "body", "", "task body")
	cmd.Flags().StringVar(&flags.assignee, "assignee", "", "assignee profile or name")
	cmd.Flags().StringVar(&flags.createdBy, "created-by", "", "task creator")
	cmd.Flags().IntVar(&flags.priority, "priority", 0, "task priority")
	cmd.Flags().StringVar(&flags.status, "status", "", "initial status")
	return cmd
}

func newKanbanListCmd(shared *kanbanSharedFlags) *cobra.Command {
	flags := &kanbanListFlags{shared: shared}
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List Kanban tasks",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runKanbanList(flags)
		},
	}
	cmd.Flags().StringVar(&flags.project, "project", "", "project id or slug")
	cmd.Flags().StringVar(&flags.board, "board", "", "board id or slug")
	cmd.Flags().StringVar(&flags.milestone, "milestone", "", "milestone id or slug")
	cmd.Flags().StringVar(&flags.status, "status", "", "task status")
	return cmd
}

func newKanbanShowCmd(shared *kanbanSharedFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "show <task>",
		Short: "Show a Kanban task",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runKanbanShow(args[0], shared)
		},
	}
	return cmd
}

func newKanbanClaimCmd(shared *kanbanSharedFlags) *cobra.Command {
	flags := &kanbanClaimFlags{shared: shared}
	cmd := &cobra.Command{
		Use:   "claim <task>",
		Short: "Claim a Kanban task for a run",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runKanbanClaim(args[0], flags)
		},
	}
	cmd.Flags().StringVar(&flags.actor, "actor", "", "actor name")
	cmd.Flags().StringVar(&flags.workDir, "work-dir", "", "run working directory")
	return cmd
}

func newKanbanCompleteCmd(shared *kanbanSharedFlags) *cobra.Command {
	flags := &kanbanCompleteFlags{shared: shared}
	cmd := &cobra.Command{
		Use:   "complete <task>",
		Short: "Complete a Kanban task",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runKanbanComplete(args[0], flags)
		},
	}
	cmd.Flags().StringVar(&flags.actor, "actor", "", "actor name")
	cmd.Flags().StringVar(&flags.summary, "summary", "", "completion summary")
	cmd.Flags().StringVar(&flags.metadata, "metadata", "", "completion metadata JSON")
	return cmd
}

func newKanbanBlockCmd(shared *kanbanSharedFlags) *cobra.Command {
	flags := &kanbanBlockFlags{shared: shared}
	cmd := &cobra.Command{
		Use:   "block <task> <reason>",
		Short: "Mark a Kanban task blocked",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runKanbanBlock(args[0], args[1], flags)
		},
	}
	cmd.Flags().StringVar(&flags.actor, "actor", "", "actor name")
	return cmd
}

func newKanbanUnblockCmd(shared *kanbanSharedFlags) *cobra.Command {
	flags := &kanbanUnblockFlags{shared: shared}
	cmd := &cobra.Command{
		Use:   "unblock <task>",
		Short: "Mark a Kanban task unblocked",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runKanbanUnblock(args[0], flags)
		},
	}
	cmd.Flags().StringVar(&flags.actor, "actor", "", "actor name")
	cmd.Flags().StringVar(&flags.comment, "comment", "", "unblock comment")
	return cmd
}

func newKanbanCommentCmd(shared *kanbanSharedFlags) *cobra.Command {
	flags := &kanbanCommentFlags{shared: shared}
	cmd := &cobra.Command{
		Use:   "comment <task> <body>",
		Short: "Add a comment to a Kanban task",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runKanbanComment(args[0], args[1], flags)
		},
	}
	cmd.Flags().StringVar(&flags.author, "author", "", "comment author")
	return cmd
}

func newKanbanLinkCmd(shared *kanbanSharedFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "link <parent> <child>",
		Short: "Link a blocking task dependency",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runKanbanLink(args[0], args[1], shared)
		},
	}
	return cmd
}

func newKanbanRunsCmd(shared *kanbanSharedFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "runs <task>",
		Short: "List runs for a Kanban task",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runKanbanRuns(args[0], shared)
		},
	}
	return cmd
}

func runKanbanCreate(title string, flags *kanbanCreateFlags) error {
	if flags == nil {
		flags = &kanbanCreateFlags{shared: &kanbanSharedFlags{}}
	}
	status, err := normalizeKanbanStatus(flags.status, true)
	if err != nil {
		return err
	}
	st, err := openKanbanCommandStore(kanbanAccountID(flags.shared))
	if err != nil {
		return err
	}
	defer st.Close()

	project, err := resolveKanbanProject(st, flags.project)
	if err != nil {
		return err
	}
	boardID, err := resolveKanbanBoardID(st, project.ID, flags.board)
	if err != nil {
		return err
	}
	milestoneID, err := resolveKanbanMilestoneID(st, project.ID, flags.milestone)
	if err != nil {
		return err
	}
	task, err := st.CreateKanbanTask(store.CreateKanbanTaskRequest{
		ProjectID:   project.ID,
		BoardID:     boardID,
		MilestoneID: milestoneID,
		Title:       title,
		Body:        flags.body,
		Status:      status,
		Priority:    flags.priority,
		Assignee:    strings.TrimSpace(flags.assignee),
		CreatedBy:   strings.TrimSpace(flags.createdBy),
	})
	if err != nil {
		return err
	}
	fmt.Printf("Created task: %s\n", task.ID)
	fmt.Printf("Project: %s\n", project.Slug)
	fmt.Printf("Status: %s\n", task.Status)
	fmt.Printf("Title: %s\n", task.Title)
	return nil
}

func runKanbanList(flags *kanbanListFlags) error {
	if flags == nil {
		flags = &kanbanListFlags{shared: &kanbanSharedFlags{}}
	}
	status, err := normalizeKanbanStatus(flags.status, true)
	if err != nil {
		return err
	}
	st, err := openKanbanCommandStore(kanbanAccountID(flags.shared))
	if err != nil {
		return err
	}
	defer st.Close()

	project, err := resolveKanbanProject(st, flags.project)
	if err != nil {
		return err
	}
	boardID, err := resolveKanbanBoardID(st, project.ID, flags.board)
	if err != nil {
		return err
	}
	milestoneID, err := resolveKanbanMilestoneID(st, project.ID, flags.milestone)
	if err != nil {
		return err
	}
	tasks, err := st.ListKanbanTasks(store.KanbanTaskListFilter{
		ProjectID:   project.ID,
		BoardID:     boardID,
		MilestoneID: milestoneID,
		Status:      status,
	})
	if err != nil {
		return err
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "ID\tStatus\tPriority\tAssignee\tTitle")
	for _, task := range tasks {
		_, _ = fmt.Fprintf(w, "%s\t%s\t%d\t%s\t%s\n", task.ID, task.Status, task.Priority, task.Assignee, task.Title)
	}
	return w.Flush()
}

func runKanbanShow(taskID string, flags *kanbanSharedFlags) error {
	st, err := openKanbanCommandStore(kanbanAccountID(flags))
	if err != nil {
		return err
	}
	defer st.Close()

	task, err := st.GetKanbanTask(strings.TrimSpace(taskID))
	if err != nil {
		return err
	}
	fmt.Printf("Task: %s\n", task.ID)
	fmt.Printf("Title: %s\n", task.Title)
	fmt.Printf("Status: %s\n", task.Status)
	fmt.Printf("Project ID: %s\n", task.ProjectID)
	fmt.Printf("Board ID: %s\n", task.BoardID)
	if task.MilestoneID != "" {
		fmt.Printf("Milestone ID: %s\n", task.MilestoneID)
	}
	if task.Assignee != "" {
		fmt.Printf("Assignee: %s\n", task.Assignee)
	}
	if task.Body != "" {
		fmt.Printf("Body: %s\n", task.Body)
	}
	return nil
}

func runKanbanClaim(taskID string, flags *kanbanClaimFlags) error {
	if flags == nil {
		flags = &kanbanClaimFlags{shared: &kanbanSharedFlags{}}
	}
	workDir, provider, err := normalizeRunWorkDir(flags.workDir)
	if err != nil {
		return err
	}
	st, err := openKanbanCommandStore(kanbanAccountID(flags.shared))
	if err != nil {
		return err
	}
	defer st.Close()

	run, err := st.ClaimKanbanTask(strings.TrimSpace(taskID), store.ClaimKanbanTaskRequest{
		Actor:           strings.TrimSpace(flags.actor),
		WorkDir:         workDir,
		WorkDirProvider: provider,
	})
	if err != nil {
		return err
	}
	fmt.Printf("Claimed task: %s\n", run.TaskID)
	fmt.Printf("Run: %s\n", run.ID)
	fmt.Printf("Work dir: %s\n", run.WorkDir)
	return nil
}

func runKanbanComplete(taskID string, flags *kanbanCompleteFlags) error {
	if flags == nil {
		flags = &kanbanCompleteFlags{shared: &kanbanSharedFlags{}}
	}
	summary := strings.TrimSpace(flags.summary)
	if summary == "" {
		return fmt.Errorf("--summary is required")
	}
	if err := validateKanbanMetadata(flags.metadata); err != nil {
		return err
	}
	st, err := openKanbanCommandStore(kanbanAccountID(flags.shared))
	if err != nil {
		return err
	}
	defer st.Close()

	if err := st.CompleteKanbanTask(strings.TrimSpace(taskID), store.CompleteKanbanTaskRequest{
		Actor:        strings.TrimSpace(flags.actor),
		Summary:      summary,
		MetadataJSON: flags.metadata,
	}); err != nil {
		return err
	}
	fmt.Printf("Completed task: %s\n", strings.TrimSpace(taskID))
	return nil
}

func runKanbanBlock(taskID, reason string, flags *kanbanBlockFlags) error {
	if flags == nil {
		flags = &kanbanBlockFlags{shared: &kanbanSharedFlags{}}
	}
	st, err := openKanbanCommandStore(kanbanAccountID(flags.shared))
	if err != nil {
		return err
	}
	defer st.Close()

	if err := st.BlockKanbanTask(strings.TrimSpace(taskID), store.BlockKanbanTaskRequest{
		Actor:  strings.TrimSpace(flags.actor),
		Reason: strings.TrimSpace(reason),
	}); err != nil {
		return err
	}
	fmt.Printf("Blocked task: %s\n", strings.TrimSpace(taskID))
	return nil
}

func runKanbanUnblock(taskID string, flags *kanbanUnblockFlags) error {
	if flags == nil {
		flags = &kanbanUnblockFlags{shared: &kanbanSharedFlags{}}
	}
	st, err := openKanbanCommandStore(kanbanAccountID(flags.shared))
	if err != nil {
		return err
	}
	defer st.Close()

	if err := st.UnblockKanbanTask(strings.TrimSpace(taskID), store.UnblockKanbanTaskRequest{
		Actor:   strings.TrimSpace(flags.actor),
		Comment: strings.TrimSpace(flags.comment),
	}); err != nil {
		return err
	}
	fmt.Printf("Unblocked task: %s\n", strings.TrimSpace(taskID))
	return nil
}

func runKanbanComment(taskID, body string, flags *kanbanCommentFlags) error {
	if flags == nil {
		flags = &kanbanCommentFlags{shared: &kanbanSharedFlags{}}
	}
	st, err := openKanbanCommandStore(kanbanAccountID(flags.shared))
	if err != nil {
		return err
	}
	defer st.Close()

	comment, err := st.AddKanbanTaskComment(strings.TrimSpace(taskID), strings.TrimSpace(flags.author), body)
	if err != nil {
		return err
	}
	fmt.Printf("Added comment: %s\n", comment.ID)
	return nil
}

func runKanbanLink(parentID, childID string, flags *kanbanSharedFlags) error {
	st, err := openKanbanCommandStore(kanbanAccountID(flags))
	if err != nil {
		return err
	}
	defer st.Close()

	if err := st.LinkKanbanTasks(strings.TrimSpace(parentID), strings.TrimSpace(childID)); err != nil {
		return err
	}
	fmt.Printf("Linked dependency: %s blocks %s\n", strings.TrimSpace(parentID), strings.TrimSpace(childID))
	return nil
}

func runKanbanRuns(taskID string, flags *kanbanSharedFlags) error {
	st, err := openKanbanCommandStore(kanbanAccountID(flags))
	if err != nil {
		return err
	}
	defer st.Close()

	runs, err := st.ListKanbanRuns(strings.TrimSpace(taskID))
	if err != nil {
		return err
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "ID\tOutcome\tActor\tWork dir\tStarted\tFinished")
	for _, run := range runs {
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n",
			run.ID, run.Outcome, run.Actor, run.WorkDir, run.StartedAt, run.FinishedAt)
	}
	return w.Flush()
}

func kanbanAccountID(flags *kanbanSharedFlags) string {
	if flags == nil {
		return ""
	}
	return flags.accountID
}

func resolveKanbanBoardID(st *store.Store, projectID, boardArg string) (string, error) {
	boardArg = strings.TrimSpace(boardArg)
	if boardArg == "" {
		return "", nil
	}
	board, err := st.GetKanbanBoard(projectID, boardArg)
	if err != nil {
		return "", fmt.Errorf("board %q: %w", boardArg, err)
	}
	return board.ID, nil
}

func resolveKanbanMilestoneID(st *store.Store, projectID, milestoneArg string) (string, error) {
	milestoneArg = strings.TrimSpace(milestoneArg)
	if milestoneArg == "" {
		return "", nil
	}
	milestone, err := st.GetKanbanMilestone(projectID, milestoneArg)
	if err != nil {
		return "", fmt.Errorf("milestone %q: %w", milestoneArg, err)
	}
	return milestone.ID, nil
}

func normalizeKanbanStatus(status string, allowEmpty bool) (string, error) {
	status = strings.TrimSpace(status)
	if status == "" && allowEmpty {
		return "", nil
	}
	switch status {
	case store.KanbanStatusTriage,
		store.KanbanStatusTodo,
		store.KanbanStatusReady,
		store.KanbanStatusRunning,
		store.KanbanStatusBlocked,
		store.KanbanStatusDone,
		store.KanbanStatusArchived:
		return status, nil
	default:
		return "", fmt.Errorf("unknown kanban status %q", status)
	}
}

func normalizeRunWorkDir(workDir string) (string, string, error) {
	workDir = strings.TrimSpace(workDir)
	if workDir == "" {
		return "", "", nil
	}
	abs, err := filepath.Abs(workDir)
	if err != nil {
		return "", "", fmt.Errorf("resolve --work-dir: %w", err)
	}
	return abs, store.KanbanWorkDirManual, nil
}

func validateKanbanMetadata(metadata string) error {
	metadata = strings.TrimSpace(metadata)
	if metadata == "" {
		return nil
	}
	if !json.Valid([]byte(metadata)) {
		return fmt.Errorf("--metadata must be valid JSON")
	}
	return nil
}
