package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	osexec "os/exec"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

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

type kanbanStaleFlags struct {
	shared     *kanbanSharedFlags
	project    string
	staleAfter string
	limit      int
}

type kanbanEditFlags struct {
	shared         *kanbanSharedFlags
	actor          string
	title          string
	titleSet       bool
	body           string
	bodySet        bool
	status         string
	statusSet      bool
	priority       int
	prioritySet    bool
	assignee       string
	assigneeSet    bool
	milestone      string
	milestoneSet   bool
	clearMilestone bool
}

type kanbanArchiveFlags struct {
	shared *kanbanSharedFlags
	actor  string
}

type kanbanClaimFlags struct {
	shared  *kanbanSharedFlags
	actor   string
	workDir string
}

type kanbanHeartbeatFlags struct {
	shared *kanbanSharedFlags
	actor  string
}

type kanbanExecFlags struct {
	shared  *kanbanSharedFlags
	actor   string
	workDir string
	summary string
}

type kanbanDispatchFlags struct {
	shared   *kanbanSharedFlags
	project  string
	actor    string
	workDir  string
	summary  string
	limit    int
	loop     bool
	interval string
}

type kanbanCompleteFlags struct {
	shared   *kanbanSharedFlags
	actor    string
	summary  string
	metadata string
}

type kanbanCancelFlags struct {
	shared   *kanbanSharedFlags
	actor    string
	metadata string
}

type kanbanReclaimFlags struct {
	shared   *kanbanSharedFlags
	actor    string
	workDir  string
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
		newKanbanStaleCmd(flags),
		newKanbanShowCmd(flags),
		newKanbanEditCmd(flags),
		newKanbanArchiveCmd(flags),
		newKanbanExecCmd(flags),
		newKanbanDispatchCmd(flags),
		newKanbanClaimCmd(flags),
		newKanbanHeartbeatCmd(flags),
		newKanbanCompleteCmd(flags),
		newKanbanCancelCmd(flags),
		newKanbanReclaimCmd(flags),
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

func newKanbanStaleCmd(shared *kanbanSharedFlags) *cobra.Command {
	flags := &kanbanStaleFlags{shared: shared}
	cmd := &cobra.Command{
		Use:   "stale",
		Short: "List stale running Kanban runs",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runKanbanStale(flags)
		},
	}
	cmd.Flags().StringVar(&flags.project, "project", "", "project id or slug")
	cmd.Flags().StringVar(&flags.staleAfter, "stale-after", "", "stale duration threshold, for example 10m or 1h")
	cmd.Flags().IntVar(&flags.limit, "limit", 50, "maximum stale runs to list")
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

func newKanbanEditCmd(shared *kanbanSharedFlags) *cobra.Command {
	flags := &kanbanEditFlags{shared: shared}
	cmd := &cobra.Command{
		Use:   "edit <task>",
		Short: "Edit a Kanban task",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			flags.titleSet = cmd.Flags().Changed("title")
			flags.bodySet = cmd.Flags().Changed("body")
			flags.statusSet = cmd.Flags().Changed("status")
			flags.prioritySet = cmd.Flags().Changed("priority")
			flags.assigneeSet = cmd.Flags().Changed("assignee")
			flags.milestoneSet = cmd.Flags().Changed("milestone")
			return runKanbanEdit(args[0], flags)
		},
	}
	cmd.Flags().StringVar(&flags.actor, "actor", "", "actor name")
	cmd.Flags().StringVar(&flags.title, "title", "", "task title")
	cmd.Flags().StringVar(&flags.body, "body", "", "task body")
	cmd.Flags().StringVar(&flags.status, "status", "", "task status")
	cmd.Flags().IntVar(&flags.priority, "priority", 0, "task priority")
	cmd.Flags().StringVar(&flags.assignee, "assignee", "", "assignee profile or name")
	cmd.Flags().StringVar(&flags.milestone, "milestone", "", "milestone id or slug")
	cmd.Flags().BoolVar(&flags.clearMilestone, "clear-milestone", false, "clear task milestone")
	return cmd
}

func newKanbanArchiveCmd(shared *kanbanSharedFlags) *cobra.Command {
	flags := &kanbanArchiveFlags{shared: shared}
	cmd := &cobra.Command{
		Use:   "archive <task>",
		Short: "Archive a Kanban task",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runKanbanArchive(args[0], flags)
		},
	}
	cmd.Flags().StringVar(&flags.actor, "actor", "", "actor name")
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

func newKanbanHeartbeatCmd(shared *kanbanSharedFlags) *cobra.Command {
	flags := &kanbanHeartbeatFlags{shared: shared}
	cmd := &cobra.Command{
		Use:   "heartbeat <task>",
		Short: "Record activity for a running Kanban task",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runKanbanHeartbeat(args[0], flags)
		},
	}
	cmd.Flags().StringVar(&flags.actor, "actor", "", "actor name")
	return cmd
}

func newKanbanExecCmd(shared *kanbanSharedFlags) *cobra.Command {
	flags := &kanbanExecFlags{shared: shared}
	cmd := &cobra.Command{
		Use:   "exec <task> -- <command> [args...]",
		Short: "Execute a command for a Kanban task",
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) < 2 {
				return fmt.Errorf("usage: kittypaw kanban exec <task> -- <command> [args...]")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return runKanbanExec(args[0], args[1:], flags)
		},
	}
	cmd.Flags().StringVar(&flags.actor, "actor", "", "actor name")
	cmd.Flags().StringVar(&flags.workDir, "work-dir", "", "run working directory")
	cmd.Flags().StringVar(&flags.summary, "summary", "", "completion summary")
	return cmd
}

func newKanbanDispatchCmd(shared *kanbanSharedFlags) *cobra.Command {
	flags := &kanbanDispatchFlags{shared: shared, limit: 1, interval: "30s"}
	cmd := &cobra.Command{
		Use:   "dispatch --project <project> -- <command> [args...]",
		Short: "Dispatch ready Kanban tasks to a command",
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return fmt.Errorf("usage: kittypaw kanban dispatch --project <project> -- <command> [args...]")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return runKanbanDispatch(cmd.Context(), args, flags)
		},
	}
	cmd.Flags().StringVar(&flags.project, "project", "", "project id or slug")
	cmd.Flags().IntVar(&flags.limit, "limit", 1, "maximum ready tasks to dispatch per cycle")
	cmd.Flags().BoolVar(&flags.loop, "loop", false, "keep polling for ready tasks")
	cmd.Flags().StringVar(&flags.interval, "interval", "30s", "poll interval when --loop is set")
	cmd.Flags().StringVar(&flags.actor, "actor", "", "actor name")
	cmd.Flags().StringVar(&flags.workDir, "work-dir", "", "run working directory")
	cmd.Flags().StringVar(&flags.summary, "summary", "", "completion summary")
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

func newKanbanCancelCmd(shared *kanbanSharedFlags) *cobra.Command {
	flags := &kanbanCancelFlags{shared: shared}
	cmd := &cobra.Command{
		Use:   "cancel <task> <reason>",
		Short: "Cancel a running Kanban task",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runKanbanCancel(args[0], args[1], flags)
		},
	}
	cmd.Flags().StringVar(&flags.actor, "actor", "", "actor name")
	cmd.Flags().StringVar(&flags.metadata, "metadata", "", "cancellation metadata JSON")
	return cmd
}

func newKanbanReclaimCmd(shared *kanbanSharedFlags) *cobra.Command {
	flags := &kanbanReclaimFlags{shared: shared}
	cmd := &cobra.Command{
		Use:   "reclaim <task> <reason>",
		Short: "Replace a stale running Kanban task run",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runKanbanReclaim(args[0], args[1], flags)
		},
	}
	cmd.Flags().StringVar(&flags.actor, "actor", "", "actor name")
	cmd.Flags().StringVar(&flags.workDir, "work-dir", "", "replacement run working directory")
	cmd.Flags().StringVar(&flags.metadata, "metadata", "", "reclaim metadata JSON")
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

func runKanbanStale(flags *kanbanStaleFlags) error {
	if flags == nil {
		flags = &kanbanStaleFlags{shared: &kanbanSharedFlags{}}
	}
	staleAfterRaw := strings.TrimSpace(flags.staleAfter)
	if staleAfterRaw == "" {
		return fmt.Errorf("--stale-after is required")
	}
	staleAfter, err := time.ParseDuration(staleAfterRaw)
	if err != nil || staleAfter <= 0 {
		return fmt.Errorf("positive --stale-after duration is required")
	}
	if flags.limit <= 0 {
		return fmt.Errorf("--limit must be positive")
	}

	st, err := openKanbanCommandStore(kanbanAccountID(flags.shared))
	if err != nil {
		return err
	}
	defer st.Close()

	projectID := ""
	if strings.TrimSpace(flags.project) != "" {
		project, err := resolveKanbanProject(st, flags.project)
		if err != nil {
			return err
		}
		projectID = project.ID
	}
	cutoff := time.Now().UTC().Add(-staleAfter).Format("2006-01-02T15:04:05Z")
	staleRuns, err := st.ListStaleKanbanRuns(store.KanbanStaleRunFilter{
		ProjectID:   projectID,
		StaleBefore: cutoff,
		Limit:       flags.limit,
	})
	if err != nil {
		return err
	}
	if len(staleRuns) == 0 {
		fmt.Println("No stale runs.")
		return nil
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "TASK\tRUN\tPROJECT\tACTOR\tHEARTBEAT\tTITLE")
	for _, item := range staleRuns {
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n",
			item.Task.ID,
			item.Run.ID,
			item.ProjectSlug,
			item.Run.Actor,
			item.Run.HeartbeatAt,
			item.Task.Title,
		)
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

func runKanbanEdit(taskID string, flags *kanbanEditFlags) error {
	if flags == nil {
		flags = &kanbanEditFlags{shared: &kanbanSharedFlags{}}
	}
	if !flags.titleSet &&
		!flags.bodySet &&
		!flags.statusSet &&
		!flags.prioritySet &&
		!flags.assigneeSet &&
		!flags.milestoneSet &&
		!flags.clearMilestone {
		return fmt.Errorf("at least one edit flag is required")
	}
	if flags.milestoneSet && flags.clearMilestone {
		return fmt.Errorf("--milestone and --clear-milestone are mutually exclusive")
	}

	var status *string
	if flags.statusSet {
		normalized, err := normalizeKanbanStatus(flags.status, false)
		if err != nil {
			return err
		}
		status = &normalized
	}

	st, err := openKanbanCommandStore(kanbanAccountID(flags.shared))
	if err != nil {
		return err
	}
	defer st.Close()

	task, err := st.GetKanbanTask(strings.TrimSpace(taskID))
	if err != nil {
		return err
	}
	req := store.UpdateKanbanTaskRequest{
		Actor:          strings.TrimSpace(flags.actor),
		Status:         status,
		ClearMilestone: flags.clearMilestone,
	}
	if flags.titleSet {
		title := strings.TrimSpace(flags.title)
		req.Title = &title
	}
	if flags.bodySet {
		body := flags.body
		req.Body = &body
	}
	if flags.prioritySet {
		priority := flags.priority
		req.Priority = &priority
	}
	if flags.assigneeSet {
		assignee := strings.TrimSpace(flags.assignee)
		req.Assignee = &assignee
	}
	if flags.milestoneSet {
		milestoneArg := strings.TrimSpace(flags.milestone)
		if milestoneArg == "" {
			return fmt.Errorf("--milestone is required when supplied")
		}
		milestoneID, err := resolveKanbanMilestoneID(st, task.ProjectID, milestoneArg)
		if err != nil {
			return err
		}
		req.MilestoneID = &milestoneID
	}

	updated, err := st.UpdateKanbanTask(task.ID, req)
	if err != nil {
		return err
	}
	fmt.Printf("Updated task: %s\n", updated.ID)
	fmt.Printf("Status: %s\n", updated.Status)
	fmt.Printf("Title: %s\n", updated.Title)
	fmt.Printf("Priority: %d\n", updated.Priority)
	if updated.Assignee != "" {
		fmt.Printf("Assignee: %s\n", updated.Assignee)
	}
	if updated.MilestoneID != "" {
		fmt.Printf("Milestone ID: %s\n", updated.MilestoneID)
	}
	return nil
}

func runKanbanArchive(taskID string, flags *kanbanArchiveFlags) error {
	if flags == nil {
		flags = &kanbanArchiveFlags{shared: &kanbanSharedFlags{}}
	}
	st, err := openKanbanCommandStore(kanbanAccountID(flags.shared))
	if err != nil {
		return err
	}
	defer st.Close()

	task, err := st.ArchiveKanbanTask(strings.TrimSpace(taskID), strings.TrimSpace(flags.actor))
	if err != nil {
		return err
	}
	fmt.Printf("Archived task: %s\n", task.ID)
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

func runKanbanHeartbeat(taskID string, flags *kanbanHeartbeatFlags) error {
	if flags == nil {
		flags = &kanbanHeartbeatFlags{shared: &kanbanSharedFlags{}}
	}
	st, err := openKanbanCommandStore(kanbanAccountID(flags.shared))
	if err != nil {
		return err
	}
	defer st.Close()

	run, err := st.HeartbeatKanbanTask(strings.TrimSpace(taskID), store.HeartbeatKanbanTaskRequest{
		Actor: strings.TrimSpace(flags.actor),
	})
	if err != nil {
		return err
	}
	fmt.Printf("Heartbeat task: %s\n", run.TaskID)
	fmt.Printf("Run: %s\n", run.ID)
	fmt.Printf("Heartbeat: %s\n", run.HeartbeatAt)
	return nil
}

func runKanbanExec(taskID string, command []string, flags *kanbanExecFlags) error {
	if flags == nil {
		flags = &kanbanExecFlags{shared: &kanbanSharedFlags{}}
	}
	if len(command) == 0 || strings.TrimSpace(command[0]) == "" {
		return fmt.Errorf("command is required")
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

	taskID = strings.TrimSpace(taskID)
	run, err := st.ClaimKanbanTask(taskID, store.ClaimKanbanTaskRequest{
		Actor:           strings.TrimSpace(flags.actor),
		WorkDir:         workDir,
		WorkDirProvider: provider,
	})
	if err != nil {
		return err
	}

	started := time.Now()
	cmd := osexec.Command(command[0], command[1:]...)
	cmd.Dir = run.WorkDir
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	runErr := cmd.Run()
	duration := time.Since(started)
	exitCode := kanbanExecExitCode(runErr)
	metadata := kanbanExecMetadata(command, run.ID, exitCode, duration)
	if runErr != nil {
		summary := strings.TrimSpace(flags.summary)
		if summary == "" {
			summary = kanbanExecDefaultSummary("command failed", command)
		}
		recordErr := st.FailKanbanTask(taskID, store.FailKanbanTaskRequest{
			Actor:        strings.TrimSpace(flags.actor),
			Summary:      summary,
			Error:        runErr.Error(),
			MetadataJSON: metadata,
		})
		if recordErr != nil {
			return fmt.Errorf("command failed (%v); record kanban failure: %w", runErr, recordErr)
		}
		return fmt.Errorf("command failed with exit code %d: %w", exitCode, runErr)
	}

	summary := strings.TrimSpace(flags.summary)
	if summary == "" {
		summary = kanbanExecDefaultSummary("command completed", command)
	}
	if err := st.CompleteKanbanTask(taskID, store.CompleteKanbanTaskRequest{
		Actor:        strings.TrimSpace(flags.actor),
		Summary:      summary,
		MetadataJSON: metadata,
	}); err != nil {
		return err
	}
	fmt.Printf("Executed task: %s\n", taskID)
	fmt.Printf("Run: %s\n", run.ID)
	fmt.Printf("Work dir: %s\n", run.WorkDir)
	return nil
}

func runKanbanDispatch(ctx context.Context, command []string, flags *kanbanDispatchFlags) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if flags == nil {
		flags = &kanbanDispatchFlags{shared: &kanbanSharedFlags{}, limit: 1, interval: "30s"}
	}
	if len(command) == 0 || strings.TrimSpace(command[0]) == "" {
		return fmt.Errorf("command is required")
	}
	if strings.TrimSpace(flags.project) == "" {
		return fmt.Errorf("--project is required")
	}
	if flags.limit <= 0 {
		return fmt.Errorf("--limit must be positive")
	}
	intervalRaw := strings.TrimSpace(flags.interval)
	if intervalRaw == "" {
		intervalRaw = "30s"
	}
	interval, err := time.ParseDuration(intervalRaw)
	if err != nil || interval <= 0 {
		return fmt.Errorf("positive --interval duration is required")
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

	project, err := resolveKanbanProject(st, flags.project)
	if err != nil {
		return err
	}

	for {
		processed, err := runKanbanDispatchCycle(ctx, st, project, command, flags, workDir, provider)
		if err != nil {
			return err
		}
		if processed == 0 && !flags.loop {
			fmt.Println("No ready tasks.")
		}
		if !flags.loop {
			return nil
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func runKanbanDispatchCycle(ctx context.Context, st *store.Store, project *store.KanbanProject, command []string, flags *kanbanDispatchFlags, workDir, provider string) (int, error) {
	tasks, err := st.ListKanbanTasks(store.KanbanTaskListFilter{
		ProjectID: project.ID,
		Status:    store.KanbanStatusReady,
	})
	if err != nil {
		return 0, err
	}
	processed := 0
	for _, task := range tasks {
		if processed >= flags.limit {
			break
		}
		if err := executeDispatchedKanbanTask(ctx, st, project, task, command, flags, workDir, provider); err != nil {
			return processed, err
		}
		processed++
	}
	return processed, nil
}

func executeDispatchedKanbanTask(ctx context.Context, st *store.Store, project *store.KanbanProject, task store.KanbanTask, command []string, flags *kanbanDispatchFlags, workDir, provider string) error {
	run, err := st.ClaimKanbanTask(task.ID, store.ClaimKanbanTaskRequest{
		Actor:           strings.TrimSpace(flags.actor),
		WorkDir:         workDir,
		WorkDirProvider: provider,
	})
	if err != nil {
		return err
	}

	started := time.Now()
	cmd := osexec.CommandContext(ctx, command[0], command[1:]...)
	cmd.Dir = run.WorkDir
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = kanbanDispatchCommandEnv(os.Environ(), project, task, run)
	runErr := cmd.Run()
	duration := time.Since(started)
	exitCode := kanbanExecExitCode(runErr)
	metadata := kanbanExecMetadata(command, run.ID, exitCode, duration)
	if runErr != nil {
		summaryPrefix := "command failed"
		if ctxErr := ctx.Err(); ctxErr != nil {
			summaryPrefix = "command canceled"
		}
		summary := strings.TrimSpace(flags.summary)
		if summary == "" {
			summary = kanbanExecDefaultSummary(summaryPrefix, command)
		}
		recordErr := st.FailKanbanTask(task.ID, store.FailKanbanTaskRequest{
			Actor:        strings.TrimSpace(flags.actor),
			Summary:      summary,
			Error:        runErr.Error(),
			MetadataJSON: metadata,
		})
		if recordErr != nil {
			return fmt.Errorf("command failed (%v); record kanban failure: %w", runErr, recordErr)
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return fmt.Errorf("command canceled: %w", ctxErr)
		}
		return fmt.Errorf("command failed with exit code %d: %w", exitCode, runErr)
	}

	summary := strings.TrimSpace(flags.summary)
	if summary == "" {
		summary = kanbanExecDefaultSummary("command completed", command)
	}
	if err := st.CompleteKanbanTask(task.ID, store.CompleteKanbanTaskRequest{
		Actor:        strings.TrimSpace(flags.actor),
		Summary:      summary,
		MetadataJSON: metadata,
	}); err != nil {
		return err
	}
	fmt.Printf("Dispatched task: %s\n", task.ID)
	fmt.Printf("Run: %s\n", run.ID)
	fmt.Printf("Work dir: %s\n", run.WorkDir)
	return nil
}

func kanbanDispatchCommandEnv(base []string, project *store.KanbanProject, task store.KanbanTask, run *store.KanbanRun) []string {
	return append(base,
		"KITTYPAW_KANBAN_TASK_ID="+task.ID,
		"KITTYPAW_KANBAN_RUN_ID="+run.ID,
		"KITTYPAW_KANBAN_PROJECT_ID="+project.ID,
		"KITTYPAW_KANBAN_PROJECT_SLUG="+project.Slug,
		"KITTYPAW_KANBAN_TASK_TITLE="+task.Title,
	)
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

func runKanbanCancel(taskID, reason string, flags *kanbanCancelFlags) error {
	if flags == nil {
		flags = &kanbanCancelFlags{shared: &kanbanSharedFlags{}}
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return fmt.Errorf("reason is required")
	}
	if err := validateKanbanMetadata(flags.metadata); err != nil {
		return err
	}
	st, err := openKanbanCommandStore(kanbanAccountID(flags.shared))
	if err != nil {
		return err
	}
	defer st.Close()

	task, err := st.CancelKanbanTask(strings.TrimSpace(taskID), store.CancelKanbanTaskRequest{
		Actor:        strings.TrimSpace(flags.actor),
		Reason:       reason,
		MetadataJSON: flags.metadata,
	})
	if err != nil {
		return err
	}
	fmt.Printf("Canceled task: %s\n", task.ID)
	fmt.Printf("Status: %s\n", task.Status)
	return nil
}

func runKanbanReclaim(taskID, reason string, flags *kanbanReclaimFlags) error {
	if flags == nil {
		flags = &kanbanReclaimFlags{shared: &kanbanSharedFlags{}}
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return fmt.Errorf("reason is required")
	}
	if err := validateKanbanMetadata(flags.metadata); err != nil {
		return err
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

	run, err := st.ReclaimKanbanTask(strings.TrimSpace(taskID), store.ReclaimKanbanTaskRequest{
		Actor:           strings.TrimSpace(flags.actor),
		Reason:          reason,
		WorkDir:         workDir,
		WorkDirProvider: provider,
		MetadataJSON:    flags.metadata,
	})
	if err != nil {
		return err
	}
	fmt.Printf("Reclaimed task: %s\n", run.TaskID)
	fmt.Printf("Run: %s\n", run.ID)
	fmt.Printf("Work dir: %s\n", run.WorkDir)
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

func kanbanExecMetadata(command []string, runID string, exitCode int, duration time.Duration) string {
	raw, err := json.Marshal(map[string]any{
		"command":     command,
		"duration_ms": duration.Milliseconds(),
		"exit_code":   exitCode,
		"run_id":      runID,
	})
	if err != nil {
		return "{}"
	}
	return string(raw)
}

func kanbanExecDefaultSummary(prefix string, command []string) string {
	return prefix + ": " + strings.Join(command, " ")
}

func kanbanExecExitCode(err error) int {
	if err == nil {
		return 0
	}
	var exitErr *osexec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}
	return -1
}
