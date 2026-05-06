// KittyPaw Kanban Board

const Kanban = {
  _container: null,
  _projects: [],
  _boards: [],
  _milestones: [],
  _tasks: [],
  _selectedProject: '',
  _selectedMilestone: '',
  _selectedTaskID: '',
  _detail: null,
  _loading: false,
  _error: '',

  _statuses: [
    { key: 'triage', label: 'Triage' },
    { key: 'todo', label: 'Todo' },
    { key: 'ready', label: 'Ready' },
    { key: 'running', label: 'Running' },
    { key: 'blocked', label: 'Blocked' },
    { key: 'done', label: 'Done' },
  ],

  mount(container) {
    this._container = container;
    this._projects = [];
    this._boards = [];
    this._milestones = [];
    this._tasks = [];
    this._selectedProject = '';
    this._selectedMilestone = '';
    this._selectedTaskID = '';
    this._detail = null;
    this._loading = false;
    this._error = '';
    this._render();
    this._loadProjects();
  },

  async _loadProjects() {
    this._loading = true;
    this._error = '';
    this._render();
    try {
      const data = await api('/api/v1/projects');
      this._projects = data.projects || [];
      if (!this._selectedProject && this._projects.length) {
        this._selectedProject = this._projectKey(this._projects[0]);
      }
      await this._loadProjectData();
    } catch (e) {
      this._setError(e);
    } finally {
      this._loading = false;
      this._render();
    }
  },

  async _loadProjectData() {
    if (!this._selectedProject) {
      this._boards = [];
      this._milestones = [];
      this._tasks = [];
      return;
    }

    const project = this._selectedProject;
    const query = this._taskQuery(project);
    const [boards, milestones, tasks] = await Promise.all([
      api('/api/v1/projects/' + encodeURIComponent(project) + '/boards'),
      api('/api/v1/projects/' + encodeURIComponent(project) + '/milestones'),
      api('/api/v1/kanban/tasks?project=' + query),
    ]);
    this._boards = boards.boards || [];
    this._milestones = milestones.milestones || [];
    this._tasks = tasks.tasks || [];
  },

  async _loadTask(taskID) {
    if (!taskID) return;
    this._selectedTaskID = taskID;
    this._error = '';
    this._render();
    try {
      this._detail = await api('/api/v1/kanban/tasks/' + encodeURIComponent(taskID));
    } catch (e) {
      this._setError(e);
    }
    this._render();
  },

  _render() {
    if (!this._container) return;

    const body = this._projects.length
      ? this._workspaceHTML()
      : this._emptyProjectHTML();

    this._container.innerHTML =
      '<div class="kanban-view">' +
        this._toolbarHTML() +
        (this._error ? '<div class="kanban-error">' + esc(this._error) + '</div>' : '') +
        (this._loading ? '<div class="kanban-loading">Loading...</div>' : body) +
      '</div>';
    this._bindEvents();
  },

  _toolbarHTML() {
    const selected = this._selectedProjectObject();
    let html = '<div class="kanban-toolbar">';
    html += '<div class="kanban-title"><h1>Kanban</h1>';
    if (selected) html += '<span>' + esc(selected.root_path || '') + '</span>';
    html += '</div>';
    html += '<div class="kanban-controls">';
    html += '<select class="input kanban-project-select" id="kanban-project-select">';
    if (!this._projects.length) {
      html += '<option value="">No projects</option>';
    } else {
      for (const project of this._projects) {
        const key = this._projectKey(project);
        html += '<option value="' + esc(key) + '"' + (key === this._selectedProject ? ' selected' : '') + '>' +
          esc(project.name || project.slug || project.id) + '</option>';
      }
    }
    html += '</select>';
    html += '<select class="input kanban-milestone-select" id="kanban-milestone-select">';
    html += '<option value="">All milestones</option>';
    for (const milestone of this._milestones) {
      const key = milestone.slug || milestone.id;
      html += '<option value="' + esc(key) + '"' + (key === this._selectedMilestone ? ' selected' : '') + '>' +
        esc(milestone.title || milestone.slug || milestone.id) + '</option>';
    }
    html += '</select>';
    html += '<button class="btn btn--secondary btn--sm" id="kanban-refresh" type="button">Refresh</button>';
    html += '</div></div>';
    return html;
  },

  _emptyProjectHTML() {
    return '<div class="kanban-empty">' +
      '<h2>No projects</h2>' +
      '<form class="kanban-form" id="kanban-project-form">' +
        '<div class="kanban-form-row">' +
          '<label>Slug<input class="input" name="slug" required></label>' +
          '<label>Name<input class="input" name="name"></label>' +
        '</div>' +
        '<label>Project root<input class="input" name="root_path" required placeholder="/absolute/path"></label>' +
        '<button class="btn btn--primary btn--sm" type="submit">Create Project</button>' +
      '</form>' +
      '</div>';
  },

  _workspaceHTML() {
    return '<div class="kanban-workspace">' +
      '<div class="kanban-board-shell">' +
      this._taskFormHTML() +
      this._boardHTML() +
      '</div>' +
      this._drawerHTML() +
      '</div>';
  },

  _taskFormHTML() {
    let html = '<form class="kanban-form kanban-task-form" id="kanban-task-form">';
    html += '<div class="kanban-form-row">';
    html += '<label>Title<input class="input" name="title" required></label>';
    html += '<label>Assignee<input class="input" name="assignee"></label>';
    html += '<label>Priority<input class="input" name="priority" type="number" value="0"></label>';
    html += '</div>';
    html += '<div class="kanban-form-row">';
    html += '<label>Status><select class="input" name="status">';
    for (const status of this._statuses) {
      html += '<option value="' + esc(status.key) + '"' + (status.key === 'todo' ? ' selected' : '') + '>' +
        esc(status.label) + '</option>';
    }
    html += '</select></label>';
    html += '<label>Milestone><select class="input" name="milestone">';
    html += '<option value="">None</option>';
    for (const milestone of this._milestones) {
      const key = milestone.slug || milestone.id;
      html += '<option value="' + esc(key) + '">' + esc(milestone.title || milestone.slug || milestone.id) + '</option>';
    }
    html += '</select></label>';
    html += '<button class="btn btn--primary btn--sm" type="submit">New Task</button>';
    html += '</div>';
    html += '<textarea class="input kanban-task-body-input" name="body" rows="2" placeholder="Body"></textarea>';
    html += '</form>';
    return html;
  },

  _boardHTML() {
    const grouped = this._tasksByStatus();
    let html = '<div class="kanban-board">';
    for (const status of this._statuses) {
      const tasks = grouped[status.key] || [];
      html += '<section class="kanban-column kanban-column--' + esc(status.key) + '">';
      html += '<div class="kanban-column-header"><div><span class="kanban-status-dot"></span>' +
        esc(status.label) + '</div><span>' + tasks.length + '</span></div>';
      html += '<div class="kanban-column-body">';
      if (tasks.length) {
        for (const task of tasks) html += this._taskCardHTML(task);
      } else {
        html += '<div class="kanban-column-empty">Empty</div>';
      }
      html += '</div></section>';
    }
    html += '</div>';
    return html;
  },

  _taskCardHTML(task) {
    const selected = task.id === this._selectedTaskID ? ' kanban-task--selected' : '';
    let html = '<button class="kanban-task' + selected + '" type="button" data-task-id="' + esc(task.id) + '">';
    html += '<span class="kanban-task-title">' + esc(task.title || task.id) + '</span>';
    html += '<span class="kanban-task-meta">';
    if (task.assignee) html += '<span>' + esc(task.assignee) + '</span>';
    html += '<span>P' + esc(String(task.priority || 0)) + '</span>';
    html += '</span>';
    html += '</button>';
    return html;
  },

  _drawerHTML() {
    if (!this._selectedTaskID) {
      return '<aside class="kanban-drawer kanban-drawer--empty"><h2>Task</h2></aside>';
    }
    if (!this._detail || !this._detail.task || this._detail.task.id !== this._selectedTaskID) {
      return '<aside class="kanban-drawer"><h2>Task</h2><div class="kanban-loading">Loading...</div></aside>';
    }
    const task = this._detail.task;
    return '<aside class="kanban-drawer">' +
      '<div class="kanban-drawer-head"><h2>' + esc(task.title || task.id) + '</h2>' +
      '<button class="btn btn--ghost btn--sm" id="kanban-close-task" type="button">Close</button></div>' +
      '<div class="kanban-drawer-meta"><span>' + esc(task.status || '') + '</span><span>' +
      esc(task.assignee || 'Unassigned') + '</span></div>' +
      '<p class="kanban-task-body">' + esc(task.body || '') + '</p>' +
      this._actionRowHTML() +
      this._commentsHTML() +
      this._runsHTML() +
      '</aside>';
  },

  _actionRowHTML() {
    return '<div class="kanban-action-row">' +
      '<button class="btn btn--secondary btn--sm" id="kanban-claim-task" type="button">Claim</button>' +
      '<button class="btn btn--primary btn--sm" id="kanban-complete-task" type="button">Complete</button>' +
      '<button class="btn btn--ghost btn--sm" id="kanban-block-task" type="button">Block</button>' +
      '<button class="btn btn--ghost btn--sm" id="kanban-unblock-task" type="button">Unblock</button>' +
      '</div>';
  },

  _commentsHTML() {
    const comments = (this._detail && this._detail.comments) || [];
    let html = '<section class="kanban-comments"><h3>Comments</h3>';
    if (comments.length) {
      html += '<div class="kanban-list">';
      for (const comment of comments) {
        html += '<div class="kanban-list-item"><div>' + esc(comment.body || '') + '</div>' +
          '<span>' + esc(comment.author || '') + '</span></div>';
      }
      html += '</div>';
    } else {
      html += '<div class="kanban-muted">No comments</div>';
    }
    html += '<form class="kanban-comment-form" id="kanban-comment-form">' +
      '<textarea class="input" name="body" rows="2" required></textarea>' +
      '<button class="btn btn--secondary btn--sm" type="submit">Comment</button>' +
      '</form></section>';
    return html;
  },

  _runsHTML() {
    const runs = (this._detail && this._detail.runs) || [];
    let html = '<section class="kanban-runs"><h3>Runs</h3>';
    if (!runs.length) return html + '<div class="kanban-muted">No runs</div></section>';
    html += '<div class="kanban-list">';
    for (const run of runs) {
      html += '<div class="kanban-list-item"><div><strong>' + esc(run.outcome || '') + '</strong> ' +
        esc(run.actor || '') + '</div>';
      if (run.work_dir) html += '<span>' + esc(run.work_dir) + '</span>';
      if (run.summary) html += '<p>' + esc(run.summary) + '</p>';
      if (run.metadata_json) html += '<pre>' + esc(run.metadata_json) + '</pre>';
      html += '</div>';
    }
    html += '</div></section>';
    return html;
  },

  _bindEvents() {
    const projectForm = document.getElementById('kanban-project-form');
    if (projectForm) {
      projectForm.addEventListener('submit', event => {
        event.preventDefault();
        this._createProject(projectForm);
      });
    }

    const taskForm = document.getElementById('kanban-task-form');
    if (taskForm) {
      taskForm.addEventListener('submit', event => {
        event.preventDefault();
        this._createTask(taskForm);
      });
    }

    const projectSelect = document.getElementById('kanban-project-select');
    if (projectSelect) {
      projectSelect.addEventListener('change', async () => {
        this._selectedProject = projectSelect.value;
        this._selectedMilestone = '';
        this._selectedTaskID = '';
        this._detail = null;
        await this._reloadProject();
      });
    }

    const milestoneSelect = document.getElementById('kanban-milestone-select');
    if (milestoneSelect) {
      milestoneSelect.addEventListener('change', async () => {
        this._selectedMilestone = milestoneSelect.value;
        this._selectedTaskID = '';
        this._detail = null;
        await this._reloadProject();
      });
    }

    const refresh = document.getElementById('kanban-refresh');
    if (refresh) refresh.addEventListener('click', () => this._reloadProject());

    this._container.querySelectorAll('[data-task-id]').forEach(button => {
      button.addEventListener('click', () => this._loadTask(button.dataset.taskId));
    });

    const close = document.getElementById('kanban-close-task');
    if (close) {
      close.addEventListener('click', () => {
        this._selectedTaskID = '';
        this._detail = null;
        this._render();
      });
    }

    const claim = document.getElementById('kanban-claim-task');
    if (claim) claim.addEventListener('click', () => this._claimTask());

    const complete = document.getElementById('kanban-complete-task');
    if (complete) complete.addEventListener('click', () => this._completeTask());

    const block = document.getElementById('kanban-block-task');
    if (block) block.addEventListener('click', () => this._blockTask());

    const unblock = document.getElementById('kanban-unblock-task');
    if (unblock) unblock.addEventListener('click', () => this._unblockTask());

    const commentForm = document.getElementById('kanban-comment-form');
    if (commentForm) {
      commentForm.addEventListener('submit', event => {
        event.preventDefault();
        this._addComment(commentForm);
      });
    }
  },

  async _createProject(form) {
    const slug = this._field(form, 'slug');
    const rootPath = this._field(form, 'root_path');
    if (!slug || !rootPath) return;
    this._loading = true;
    this._error = '';
    this._render();
    try {
      const data = await this._postJSON('/api/v1/projects', {
        slug: slug,
        name: this._field(form, 'name'),
        root_path: rootPath,
      });
      const project = data.project || {};
      this._selectedProject = project.slug || project.id || slug;
      await this._loadProjects();
    } catch (e) {
      this._setError(e);
    } finally {
      this._loading = false;
      this._render();
    }
  },

  async _createTask(form) {
    if (!this._selectedProject) return;
    const title = this._field(form, 'title');
    if (!title) return;
    this._loading = true;
    this._error = '';
    this._render();
    try {
      const data = await this._postJSON('/api/v1/kanban/tasks', {
        project: this._selectedProject,
        milestone: this._field(form, 'milestone'),
        title: title,
        body: this._field(form, 'body'),
        status: this._field(form, 'status') || 'todo',
        priority: parseInt(this._field(form, 'priority') || '0', 10) || 0,
        assignee: this._field(form, 'assignee'),
        created_by: 'web',
      });
      const task = data.task || {};
      this._selectedTaskID = task.id || '';
      await this._loadProjectData();
      if (this._selectedTaskID) {
        this._detail = await api('/api/v1/kanban/tasks/' + encodeURIComponent(this._selectedTaskID));
      }
    } catch (e) {
      this._setError(e);
    } finally {
      this._loading = false;
      this._render();
    }
  },

  async _claimTask() {
    await this._taskAction('/claim', { actor: 'web' });
  },

  async _completeTask() {
    const summary = prompt('Summary');
    if (!summary) return;
    await this._taskAction('/complete', { actor: 'web', summary: summary, metadata: {} });
  },

  async _blockTask() {
    const reason = prompt('Reason');
    if (!reason) return;
    await this._taskAction('/block', { actor: 'web', reason: reason });
  },

  async _unblockTask() {
    const comment = prompt('Comment') || '';
    await this._taskAction('/unblock', { actor: 'web', comment: comment });
  },

  async _addComment(form) {
    if (!this._selectedTaskID) return;
    const body = this._field(form, 'body');
    if (!body) return;
    try {
      await this._postJSON('/api/v1/kanban/tasks/' + encodeURIComponent(this._selectedTaskID) + '/comments', {
        author: 'web',
        body: body,
      });
      await this._loadTask(this._selectedTaskID);
    } catch (e) {
      this._setError(e);
      this._render();
    }
  },

  async _taskAction(path, body) {
    if (!this._selectedTaskID) return;
    this._error = '';
    try {
      await this._postJSON('/api/v1/kanban/tasks/' + encodeURIComponent(this._selectedTaskID) + path, body);
      await this._loadProjectData();
      this._detail = await api('/api/v1/kanban/tasks/' + encodeURIComponent(this._selectedTaskID));
    } catch (e) {
      this._setError(e);
    }
    this._render();
  },

  async _postJSON(url, body) {
    return api(url, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body),
    });
  },

  async _reloadProject() {
    this._loading = true;
    this._error = '';
    this._render();
    try {
      await this._loadProjectData();
    } catch (e) {
      this._setError(e);
    } finally {
      this._loading = false;
      this._render();
    }
  },

  _projectKey(project) {
    return project.slug || project.id || '';
  },

  _selectedProjectObject() {
    return this._projects.find(project => this._projectKey(project) === this._selectedProject) || null;
  },

  _taskQuery(project) {
    let query = encodeURIComponent(project);
    if (this._selectedMilestone) {
      query += '&milestone=' + encodeURIComponent(this._selectedMilestone);
    }
    return query;
  },

  _tasksByStatus() {
    const grouped = {};
    for (const status of this._statuses) grouped[status.key] = [];
    for (const task of this._tasks) {
      const key = grouped[task.status] ? task.status : 'todo';
      grouped[key].push(task);
    }
    return grouped;
  },

  _setError(error) {
    this._error = error && error.message ? error.message : String(error || 'Request failed');
  },

  _field(form, name) {
    const field = form.elements[name];
    return field ? field.value.trim() : '';
  },
};
