// KittyPaw Projects Board

function projectsT(key, params, fallback) {
  const runtime = typeof window !== 'undefined' ? window.KittyPawI18n : null;
  const value = runtime && typeof runtime.t === 'function' ? runtime.t(key, params) : key;
  return value === key && fallback ? fallback : value;
}

const Projects = {
  _container: null,
  _projects: [],
  _drivers: [],
  _board: null,
  _tickets: [],
  _jobs: [],
  _selectedProject: '',
  _selectedTicketID: '',
  _selectedJob: '',
  _jobLogs: null,
  _detail: null,
  _activeProjectTab: 'board',
  _projectKickoffMessage: '',
  _loading: false,
  _error: '',
  _notice: '',
  _selectedProjectPath: '',
  _directoryPickerRequestID: 0,
  _projectFieldsAuto: true,

  _statuses: [
    { key: 'draft', label: 'Draft' },
    { key: 'backlog', label: 'Backlog' },
    { key: 'ready', label: 'Ready' },
    { key: 'in_progress', label: 'In Progress' },
    { key: 'blocked', label: 'Blocked' },
    { key: 'review', label: 'Review' },
    { key: 'done', label: 'Done' },
  ],

  mount(container) {
    this._container = container;
    this._projects = [];
    this._drivers = [];
    this._board = null;
    this._tickets = [];
    this._jobs = [];
    this._selectedProject = '';
    this._selectedTicketID = '';
    this._selectedJob = '';
    this._jobLogs = null;
    this._detail = null;
    this._activeProjectTab = 'board';
    this._projectKickoffMessage = '';
    this._loading = false;
    this._error = '';
    this._notice = '';
    this._selectedProjectPath = '';
    this._directoryPickerRequestID = 0;
    this._projectFieldsAuto = true;
    this._render();
    this._loadProjects();
  },

  async _loadProjects() {
    this._loading = true;
    this._error = '';
    this._render();
    try {
      const [projectData, driverData] = await Promise.all([
        api('/api/v1/projects'),
        api('/api/v1/drivers'),
      ]);
      this._projects = projectData.projects || [];
      this._drivers = driverData.drivers || [];
      if (!this._selectedProject && this._projects.length) {
        this._selectedProject = this._projectKey(this._projects[0]);
      }
      await this._loadProjectBoard();
    } catch (e) {
      this._setError(e);
    } finally {
      this._loading = false;
      this._render();
    }
  },

  async _loadProjectBoard() {
    if (!this._selectedProject) {
      this._board = null;
      this._tickets = [];
      return;
    }
    const projectKey = this._selectedProject;
    const boardData = await api('/api/v1/projects/' + encodeURIComponent(projectKey) + '/board');
    this._board = boardData.board || null;
    const columns = this._board && this._board.columns ? this._board.columns : {};
    this._tickets = [];
    for (const status of this._statuses) {
      this._tickets.push(...(columns[status.key] || []));
    }
    if (this._selectedTicketID && !this._tickets.some(ticket => ticket.id === this._selectedTicketID)) {
      this._selectedTicketID = '';
      this._selectedJob = '';
      this._jobs = [];
      this._jobLogs = null;
      this._detail = null;
    }
  },

  async _loadTicket(ticketID) {
    if (!ticketID) return;
    this._selectedTicketID = ticketID;
    this._error = '';
    this._render();
    try {
      const [detail, jobsData] = await Promise.all([
        api('/api/v1/tickets/' + encodeURIComponent(ticketID)),
        api('/api/v1/tickets/' + encodeURIComponent(ticketID) + '/jobs'),
      ]);
      this._detail = detail;
      this._jobs = jobsData.jobs || [];
      if (this._selectedJob && !this._jobs.some(job => job.id === this._selectedJob)) {
        this._selectedJob = '';
        this._jobLogs = null;
      }
      if (!this._selectedJob && this._jobs.length) {
        this._selectedJob = this._jobs[0].id || '';
      }
    } catch (e) {
      this._setError(e);
    }
    this._render();
  },

  _render() {
    if (!this._container) return;
    if (!this._projects.length && !this._loading) {
      this._renderProjectForm();
      return;
    }
    this._container.innerHTML =
      '<div class="projects-view">' +
        this._toolbarHTML() +
        (this._error ? '<div class="projects-error">' + esc(this._error) + '</div>' : '') +
        (this._notice ? '<div class="projects-kickoff-message">' + esc(this._notice) + '</div>' : '') +
        (this._loading ? '<div class="projects-loading">' + esc(projectsT('common.loading', null, 'Loading...')) + '</div>' : this._boardLayoutHTML()) +
      '</div>';
    this._bindEvents();
  },

  _toolbarHTML() {
    const selected = this._selectedProjectObject();
    let html = '<div class="projects-toolbar">';
    html += '<div class="projects-title"><h1>' + esc(projectsT('projects.title', null, 'Projects')) + '</h1>';
    if (selected) html += '<span>' + esc(selected.root_path || '') + '</span>';
    html += '</div>';
    html += '<div class="projects-controls">';
    html += '<select class="input projects-project-select" id="projects-project-select">';
    for (const project of this._projects) {
      const key = this._projectKey(project);
      html += '<option value="' + escHTMLAttr(key) + '"' + (key === this._selectedProject ? ' selected' : '') + '>' +
        esc(project.name || project.key || project.id) + '</option>';
    }
    html += '</select>';
    html += '<button class="btn btn--secondary btn--sm" id="projects-new-project" type="button">' + esc(projectsT('projects.newProject', null, 'New Project')) + '</button>';
    html += '<button class="btn btn--secondary btn--sm" id="projects-refresh" type="button">' + esc(projectsT('common.refresh', null, 'Refresh')) + '</button>';
    html += '</div></div>';
    return html;
  },

  _boardLayoutHTML() {
    return this._projectTabsHTML() +
      (this._activeProjectTab === 'chat' ? this._projectChatHTML(this._selectedProjectObject()) : this._boardTabHTML());
  },

  _projectTabsHTML() {
    const tabs = [
      { key: 'board', label: projectsT('projects.board', null, 'Board') },
      { key: 'chat', label: projectsT('projects.projectChat', null, 'Project Chat') },
    ];
    let html = '<div class="projects-tabs">';
    for (const tab of tabs) {
      const active = this._activeProjectTab === tab.key ? ' projects-tab--active' : '';
      html += '<button class="projects-tab' + escHTMLAttr(active) + '" type="button" data-project-tab="' + escHTMLAttr(tab.key) + '">' + esc(tab.label) + '</button>';
    }
    html += '</div>';
    return html;
  },

  _boardTabHTML() {
    return '<div class="projects-layout">' +
      '<div class="projects-board-shell">' +
      this._ticketFormHTML() +
      this._boardHTML() +
      '</div>' +
      this._drawerHTML() +
      '</div>';
  },

  _projectChatHTML(project) {
    if (!project) {
      return '<section class="projects-chat-panel"></section>';
    }
    const kickoff = this._projectKickoffMessage ? '<div class="projects-kickoff-message">' + esc(this._projectKickoffMessage) + '</div>' : '';
    return '<section class="projects-chat-panel">' +
      kickoff +
      '<div id="projects-project-chat-panel"></div>' +
      '</section>';
  },

  _ticketFormHTML() {
    let html = '<form class="projects-form projects-ticket-form" id="projects-ticket-form">';
    html += '<div class="projects-form-row">';
    html += '<label>' + esc(projectsT('projects.ticketTitle', null, 'Title')) + '<input class="input" name="title" required></label>';
    html += '<label>' + esc(projectsT('projects.status', null, 'Status')) + '<select class="input" name="status">';
    for (const status of this._statuses) {
      html += '<option value="' + escHTMLAttr(status.key) + '"' + (status.key === 'backlog' ? ' selected' : '') + '>' + esc(this._statusLabel(status)) + '</option>';
    }
    html += '</select></label>';
    html += '<label>' + esc(projectsT('projects.priority', null, 'Priority')) + '<input class="input" name="priority" type="number" value="0"></label>';
    html += '<button class="btn btn--primary btn--sm" type="submit">' + esc(projectsT('projects.newTicket', null, 'New Ticket')) + '</button>';
    html += '</div>';
    html += '<textarea class="input projects-ticket-body-input" name="body" rows="2" placeholder="' + escHTMLAttr(projectsT('projects.ticketBody', null, 'Body')) + '"></textarea>';
    html += '</form>';
    return html;
  },

  _boardHTML() {
    const grouped = this._ticketsByStatus();
    let html = '<div class="projects-board">';
    for (const status of this._statuses) {
      const tickets = grouped[status.key] || [];
      html += '<section class="projects-column projects-column--' + escHTMLAttr(status.key) + '">';
      html += '<div class="projects-column-header"><div><span class="projects-status-dot"></span>' +
        esc(this._statusLabel(status)) + '</div><span>' + tickets.length + '</span></div>';
      html += '<div class="projects-column-body">';
      if (tickets.length) {
        for (const ticket of tickets) html += this._ticketCardHTML(ticket);
      } else {
        html += '<div class="projects-column-empty">' + esc(projectsT('projects.empty', null, 'Empty')) + '</div>';
      }
      html += '</div></section>';
    }
    html += '</div>';
    return html;
  },

  _ticketCardHTML(ticket) {
    const selected = ticket.id === this._selectedTicketID ? ' projects-ticket--selected' : '';
    let html = '<button class="projects-ticket' + escHTMLAttr(selected) + '" type="button" data-ticket-id="' + escHTMLAttr(ticket.id) + '">';
    html += '<span class="projects-ticket-title">' + esc(ticket.title || ticket.key || ticket.id) + '</span>';
    html += '<span class="projects-ticket-meta"><span>' + esc(ticket.key || '') + '</span><span>P' + esc(String(ticket.priority || 0)) + '</span></span>';
    html += '</button>';
    return html;
  },

  _drawerHTML() {
    if (!this._selectedTicketID) {
      return '<aside class="projects-drawer projects-drawer--empty"><h2>' + esc(projectsT('projects.ticket', null, 'Ticket')) + '</h2></aside>';
    }
    if (!this._detail || !this._detail.ticket || this._detail.ticket.id !== this._selectedTicketID) {
      return '<aside class="projects-drawer"><h2>' + esc(projectsT('projects.ticket', null, 'Ticket')) + '</h2><div class="projects-loading">' + esc(projectsT('common.loading', null, 'Loading...')) + '</div></aside>';
    }
    const ticket = this._detail.ticket;
    return '<aside class="projects-drawer">' +
      '<div class="projects-drawer-head"><h2>' + esc(ticket.title || ticket.key || ticket.id) + '</h2>' +
      '<button class="btn btn--ghost btn--sm" id="projects-close-ticket" type="button">' + esc(projectsT('common.close', null, 'Close')) + '</button></div>' +
      '<div class="projects-drawer-meta"><span>' + esc(ticket.key || '') + '</span><span>' + esc(this._statusLabelForKey(ticket.status)) + '</span></div>' +
      '<p class="projects-ticket-body">' + esc(ticket.body || '') + '</p>' +
      this._ticketActionsHTML(ticket) +
      this._ticketChatHTML(ticket) +
      this._jobSectionHTML() +
      '</aside>';
  },

  _ticketChatHTML(ticket) {
    if (!ticket) return '';
    return '<section class="projects-ticket-chat">' +
      '<h3>' + esc(projectsT('projects.ticketChat', null, 'Ticket Chat')) + '</h3>' +
      '<div id="projects-ticket-chat-panel"></div>' +
      '</section>';
  },

  _ticketActionsHTML(ticket) {
    let html = '<form class="projects-form projects-edit-form" id="projects-edit-form">';
    html += '<div class="projects-form-row projects-form-row--compact">';
    html += '<label>' + esc(projectsT('projects.status', null, 'Status')) + '<select class="input" name="status">';
    for (const status of this._statuses) {
      html += '<option value="' + escHTMLAttr(status.key) + '"' + (status.key === ticket.status ? ' selected' : '') + '>' + esc(this._statusLabel(status)) + '</option>';
    }
    html += '</select></label>';
    html += '<button class="btn btn--primary btn--sm" type="submit">' + esc(projectsT('common.save', null, 'Save')) + '</button>';
    html += '<button class="btn btn--ghost btn--sm" id="projects-archive-ticket" type="button">' + esc(projectsT('projects.archive', null, 'Archive')) + '</button>';
    html += '</div></form>';
    return html;
  },

  _jobSectionHTML() {
    let html = '<section class="projects-jobs"><h3>' + esc(projectsT('projects.jobs', null, 'Jobs')) + '</h3>';
    html += '<div class="projects-form-row projects-form-row--compact">';
    html += '<label>' + esc(projectsT('projects.drivers', null, 'Drivers')) + '<select class="input" id="projects-job-driver">';
    const drivers = this._drivers.length ? this._drivers : [{ id: 'codex', display_name: 'Codex' }];
    for (const driver of drivers) {
      html += '<option value="' + escHTMLAttr(driver.id || '') + '">' + esc(driver.display_name || driver.id || '') + '</option>';
    }
    html += '</select></label>';
    html += '<button class="btn btn--secondary btn--sm" id="projects-plan-job" type="button">' + esc(projectsT('projects.planJob', null, 'Plan Job')) + '</button>';
    html += '</div>';
    html += '<div class="projects-job-list">';
    for (const job of this._jobs || []) {
      const active = this._selectedJob === job.id ? ' projects-job--active' : '';
      html += '<button class="projects-job' + escHTMLAttr(active) + '" data-projects-job="' + escHTMLAttr(job.id || '') + '" type="button">' +
        '<span>' + esc(job.prompt_summary || job.id || '') + '</span>' +
        '<small>' + esc(job.status || '') + ' · ' + esc(job.driver_id || '') + '</small>' +
        '</button>';
    }
    html += '</div>';
    html += this._jobDetailHTML();
    html += '</section>';
    return html;
  },

  _jobDetailHTML() {
    const job = this._currentJob();
    if (!job) return '';
    const logs = this._jobLogs && this._jobLogs.job && this._jobLogs.job.id === job.id ? this._jobLogs : null;
    const current = logs && logs.job ? logs.job : job;
    let html = '<section class="projects-job-detail">';
    html += '<h4>' + esc(current.prompt_summary || current.id || '') + '</h4>';
    html += '<dl class="projects-job-meta">';
    html += '<dt>Status</dt><dd>' + esc(current.status || '') + '</dd>';
    html += '<dt>Driver</dt><dd>' + esc(current.driver_id || '') + ' / ' + esc(current.mode || '') + '</dd>';
    html += '<dt>Branch</dt><dd>' + esc(current.branch_name || '') + '</dd>';
    html += '<dt>Worktree</dt><dd>' + esc(current.worktree_path || '') + '</dd>';
    html += '</dl>';
    if (current.status === 'approved') html += '<button class="btn btn--primary btn--sm" id="projects-job-start" type="button">Start</button>';
    if (current.status === 'running') html += '<button class="btn btn--secondary btn--sm" id="projects-job-cancel" type="button">Cancel</button>';
    if (current.worktree_path) html += '<button class="btn btn--secondary btn--sm" id="projects-job-open-worktree" type="button">Open Worktree</button>';
    const logTail = (logs && logs.log_tail) || current.log_tail || '';
    if (logTail) html += '<pre class="projects-job-log">' + esc(logTail) + '</pre>';
    html += '</section>';
    return html;
  },

  _renderProjectForm() {
    if (!this._container) return;
    this._container.innerHTML = '<div class="projects-view">' +
      '<div class="projects-empty">' +
        '<h1>' + esc(projectsT('projects.title', null, 'Projects')) + '</h1>' +
        '<form class="projects-form projects-project-form" id="projects-project-form">' +
          '<div class="projects-form-row">' +
            '<label>' + esc(projectsT('projects.projectKey', null, 'Key')) + '<input class="input input--mono" id="projects-project-key" autocomplete="off"></label>' +
            '<label>' + esc(projectsT('projects.projectName', null, 'Name')) + '<input class="input" id="projects-project-name" autocomplete="off"></label>' +
          '</div>' +
          '<label>Project Folder</label>' +
          '<input class="input input--mono" id="projects-folder-path" autocomplete="off" spellcheck="false">' +
          '<div class="projects-dir-picker">' +
            '<div class="projects-dir-body">' +
              '<div class="projects-dir-sidebar">' +
                '<button class="btn btn--ghost btn--sm projects-dir-up" id="projects-directory-parent" type="button" disabled>' + esc(projectsT('settings.up', null, 'Up')) + '</button>' +
                '<div class="projects-dir-breadcrumb" id="projects-directory-breadcrumb"></div>' +
              '</div>' +
              '<div class="projects-dir-main"><div class="projects-dir-list" id="projects-directory-list"></div></div>' +
            '</div>' +
            '<div class="projects-dir-footer">' +
              '<span class="projects-dir-footer-label">' + esc(projectsT('settings.selected', null, 'Selected')) + '</span>' +
              '<span class="projects-dir-selected-path" id="projects-folder-selected"></span>' +
            '</div>' +
          '</div>' +
          '<div class="projects-actions">' +
            '<button class="btn btn--primary btn--sm" id="projects-project-save" type="button">' + esc(projectsT('projects.createProject', null, 'Create Project')) + '</button>' +
            '<button class="btn btn--ghost btn--sm" id="projects-project-cancel" type="button">' + esc(projectsT('common.cancel', null, 'Cancel')) + '</button>' +
          '</div>' +
          '<div class="error-box mt-12" id="projects-form-error" hidden></div>' +
        '</form>' +
      '</div>' +
    '</div>';
    this._bindProjectForm();
  },

  _bindEvents() {
    const projectSelect = document.getElementById('projects-project-select');
    if (projectSelect) {
      projectSelect.onchange = async () => {
        this._selectedProject = projectSelect.value;
        this._selectedTicketID = '';
        this._selectedJob = '';
        this._jobs = [];
        this._jobLogs = null;
        this._detail = null;
        this._projectKickoffMessage = '';
        await this._loadProjectBoard();
        this._render();
      };
    }
    document.querySelectorAll('[data-project-tab]').forEach(button => {
      button.onclick = () => {
        this._activeProjectTab = button.dataset.projectTab || 'board';
        this._render();
      };
    });
    const refresh = document.getElementById('projects-refresh');
    if (refresh) refresh.onclick = () => this._loadProjects();
    const newProject = document.getElementById('projects-new-project');
    if (newProject) newProject.onclick = () => this._renderProjectForm();
    const ticketForm = document.getElementById('projects-ticket-form');
    if (ticketForm) ticketForm.onsubmit = event => this._createTicket(event, ticketForm);
    document.querySelectorAll('[data-ticket-id]').forEach(button => {
      button.onclick = () => this._loadTicket(button.dataset.ticketId || '');
    });
    const close = document.getElementById('projects-close-ticket');
    if (close) close.onclick = () => {
      this._selectedTicketID = '';
      this._selectedJob = '';
      this._jobs = [];
      this._jobLogs = null;
      this._detail = null;
      this._render();
    };
    const editForm = document.getElementById('projects-edit-form');
    if (editForm) editForm.onsubmit = event => this._moveTicket(event, editForm);
    const archive = document.getElementById('projects-archive-ticket');
    if (archive) archive.onclick = () => this._archiveTicket();
    const planJob = document.getElementById('projects-plan-job');
    if (planJob) planJob.onclick = () => this._planJob();
    document.querySelectorAll('[data-projects-job]').forEach(button => {
      button.onclick = async () => {
        this._selectedJob = button.dataset.projectsJob || '';
        await this._loadJobLogs(this._selectedJob);
        this._render();
      };
    });
    const startJob = document.getElementById('projects-job-start');
    if (startJob) startJob.onclick = () => this._startJob();
    const cancelJob = document.getElementById('projects-job-cancel');
    if (cancelJob) cancelJob.onclick = () => this._cancelJob();
    const openWorktree = document.getElementById('projects-job-open-worktree');
    if (openWorktree) openWorktree.onclick = () => this._openSelectedJobWorktree();
    const project = this._selectedProjectObject();
    const projectChatPanel = document.getElementById('projects-project-chat-panel');
    if (this._activeProjectTab === 'chat' && project && projectChatPanel && typeof Chat !== 'undefined') {
      Chat.mount(projectChatPanel, { conversationID: project.project_conversation_id || '' });
    }
    const ticket = this._detail && this._detail.ticket ? this._detail.ticket : null;
    const ticketChatPanel = document.getElementById('projects-ticket-chat-panel');
    if (this._activeProjectTab === 'board' && ticket && ticketChatPanel && typeof Chat !== 'undefined') {
      Chat.mount(ticketChatPanel, { conversationID: ticket.ticket_conversation_id || '' });
    }
  },

  _bindProjectForm() {
    const keyInput = document.getElementById('projects-project-key');
    const nameInput = document.getElementById('projects-project-name');
    const pathInput = document.getElementById('projects-folder-path');
    if (keyInput) keyInput.addEventListener('input', () => { this._projectFieldsAuto = false; });
    if (nameInput) nameInput.addEventListener('input', () => { this._projectFieldsAuto = false; });
    if (pathInput) {
      pathInput.addEventListener('keydown', (event) => {
        if (event.key !== 'Enter') return;
        event.preventDefault();
        this._loadDirectoryPicker(pathInput.value.trim());
      });
    }
    const cancel = document.getElementById('projects-project-cancel');
    if (cancel) cancel.onclick = () => this._render();
    const save = document.getElementById('projects-project-save');
    if (save) save.onclick = async () => {
      const button = document.getElementById('projects-project-save');
      const error = document.getElementById('projects-form-error');
      const folderInput = document.getElementById('projects-folder-path');
      button.disabled = true;
      if (error) error.hidden = true;
      try {
        if (document.getElementById('projects-project-key').value.trim() || document.getElementById('projects-project-name').value.trim()) {
          this._projectFieldsAuto = false;
        }
        const projectPath = await this._resolveProjectPathForSave(folderInput);
        if (!projectPath) throw new Error(projectsT('projects.selectProjectFolder', null, 'Select a project folder.'));
        const created = await api('/api/v1/projects', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({
            key: document.getElementById('projects-project-key').value.trim(),
            name: document.getElementById('projects-project-name').value.trim(),
            root_path: projectPath,
          }),
        });
        if (created && created.project) {
          this._selectedProject = this._projectKey(created.project);
          this._activeProjectTab = 'chat';
          this._projectKickoffMessage = created.kickoff_message || '';
        }
        await this._loadProjects();
      } catch (e) {
        if (error) {
          error.textContent = String(e.message || e);
          error.hidden = false;
        }
      } finally {
        button.disabled = false;
      }
    };
    this._loadDirectoryPicker('');
  },

  async _createTicket(event, form) {
    event.preventDefault();
    const project = this._selectedProjectObject();
    if (!project) return;
    try {
      await api('/api/v1/tickets', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          project: this._projectKey(project),
          title: this._field(form, 'title'),
          body: this._field(form, 'body'),
          status: this._field(form, 'status'),
          priority: Number(this._field(form, 'priority') || 0),
          created_by: 'web',
        }),
      });
      form.reset();
      await this._loadProjectBoard();
      this._render();
    } catch (e) {
      this._setError(e);
      this._render();
    }
  },

  async _moveTicket(event, form) {
    event.preventDefault();
    if (!this._selectedTicketID) return;
    try {
      await api('/api/v1/tickets/' + encodeURIComponent(this._selectedTicketID) + '/actions', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ type: 'move', status: this._field(form, 'status'), actor_id: 'web' }),
      });
      await this._loadProjectBoard();
      await this._loadTicket(this._selectedTicketID);
    } catch (e) {
      this._setError(e);
      this._render();
    }
  },

  async _archiveTicket() {
    if (!this._selectedTicketID) return;
    try {
      await api('/api/v1/tickets/' + encodeURIComponent(this._selectedTicketID) + '/archive', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ actor_id: 'web' }),
      });
      this._selectedTicketID = '';
      this._detail = null;
      await this._loadProjectBoard();
      this._render();
    } catch (e) {
      this._setError(e);
      this._render();
    }
  },

  async _planJob() {
    if (!this._selectedTicketID || !this._detail || !this._detail.ticket) return;
    const ticket = this._detail.ticket;
    const driver = document.getElementById('projects-job-driver');
    try {
      await api('/api/v1/tickets/' + encodeURIComponent(this._selectedTicketID) + '/jobs/plan', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          driver_id: driver ? driver.value : 'codex',
          mode: 'one_shot',
          prompt_summary: ticket.title || ticket.key || '',
          prompt_text: ticket.body || ticket.title || '',
          created_by: 'web',
        }),
      });
      await this._loadTicket(this._selectedTicketID);
    } catch (e) {
      this._setError(e);
      this._render();
    }
  },

  async _startJob() {
    if (!this._selectedJob) return;
    try {
      const res = await fetch('/api/v1/jobs/' + encodeURIComponent(this._selectedJob) + '/start', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ actor_id: 'web' }),
      });
      const data = await res.json();
      if (!res.ok && data.code === 'project_not_git_repository') {
        await this._promptGitInit();
        return;
      }
      if (!res.ok) throw new Error(data.error || 'Job start failed');
      await this._loadJobLogs(this._selectedJob);
      await this._loadProjectBoard();
    } catch (e) {
      this._setError(e);
    }
    this._render();
  },

  async _cancelJob() {
    if (!this._selectedJob) return;
    try {
      await api('/api/v1/jobs/' + encodeURIComponent(this._selectedJob) + '/cancel', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ actor_id: 'web', reason: 'canceled from Projects UI' }),
      });
      await this._loadJobLogs(this._selectedJob);
      await this._loadProjectBoard();
    } catch (e) {
      this._setError(e);
    }
    this._render();
  },

  async _loadJobLogs(jobID) {
    if (!jobID) return null;
    const data = await api('/api/v1/jobs/' + encodeURIComponent(jobID) + '/logs');
    this._jobLogs = data;
    if (data && data.job) {
      this._jobs = (this._jobs || []).map(job => job.id === data.job.id ? data.job : job);
    }
    return data;
  },

  async _promptGitInit() {
    const project = this._selectedProjectObject();
    if (!project) return;
    if (!window.confirm('This project is not a git repository. Initialize git for this project?')) return;
    const res = await fetch('/api/v1/projects/' + encodeURIComponent(project.id || project.key) + '/git/init', { method: 'POST' });
    const data = await res.json();
    if (!res.ok) throw new Error(data.error || 'Git init failed');
    this._notice = data.git && !data.git.has_head ? 'Create an initial commit before starting a job.' : 'Git initialized.';
  },

  _openSelectedJobWorktree() {
    const job = this._currentJob();
    if (job && job.worktree_path) {
      this._notice = job.worktree_path;
      this._render();
    }
  },

  _currentJob() {
    if (!this._selectedJob && this._jobs && this._jobs.length) {
      return this._jobs[0];
    }
    const logs = this._jobLogs && this._jobLogs.job ? this._jobLogs.job : null;
    if (logs && logs.id === this._selectedJob) return logs;
    return (this._jobs || []).find(job => job.id === this._selectedJob) || null;
  },

  async _loadDirectoryPicker(path) {
    const list = document.getElementById('projects-directory-list');
    const pathInput = document.getElementById('projects-folder-path');
    const selected = document.getElementById('projects-folder-selected');
    const parentButton = document.getElementById('projects-directory-parent');
    const breadcrumb = document.getElementById('projects-directory-breadcrumb');
    const error = document.getElementById('projects-form-error');
    if (!list || !pathInput || !selected || !parentButton || !breadcrumb) return false;

    const requestID = ++this._directoryPickerRequestID;
    const previousPath = this._selectedProjectPath;
    pathInput.classList.add('is-loading');
    if (error) error.hidden = true;
    try {
      const suffix = path ? `?path=${encodeURIComponent(path)}` : '';
      const data = await apiRaw(`/api/settings/directories${suffix}`);
      if (requestID !== this._directoryPickerRequestID) return false;
      this._selectedProjectPath = data.path || '';
      pathInput.value = this._selectedProjectPath;
      selected.textContent = this._selectedProjectPath || projectsT('projects.noFolderSelected', null, 'No folder selected');
      this._renderDirectoryBreadcrumb(breadcrumb, this._selectedProjectPath);
      this._suggestProjectFields(this._selectedProjectPath);

      parentButton.disabled = !data.parent;
      parentButton.onclick = () => {
        if (data.parent) this._loadDirectoryPicker(data.parent);
      };
      this._renderDirectoryEntries(list, Array.isArray(data.entries) ? data.entries : []);
      return true;
    } catch (e) {
      if (requestID !== this._directoryPickerRequestID) return false;
      this._selectedProjectPath = previousPath;
      pathInput.value = previousPath;
      selected.textContent = previousPath || projectsT('projects.noFolderSelected', null, 'No folder selected');
      this._renderDirectoryBreadcrumb(breadcrumb, previousPath);
      if (error) {
        error.textContent = String(e.message || e);
        error.hidden = false;
      }
      return false;
    } finally {
      if (requestID === this._directoryPickerRequestID) {
        pathInput.classList.remove('is-loading');
      }
    }
  },

  async _resolveProjectPathForSave(pathInput) {
    if (!pathInput) return this._selectedProjectPath;
    const requestedPath = pathInput.value.trim();
    if (!requestedPath) return '';
    if (requestedPath !== this._selectedProjectPath) {
      const resolved = await this._loadDirectoryPicker(requestedPath);
      if (!resolved) return '';
    }
    return this._selectedProjectPath;
  },

  _renderDirectoryEntries(container, entries) {
    if (!entries.length) {
      this._renderDirectoryEmpty(container, projectsT('projects.noFolders', null, 'No folders'));
      return;
    }
    const fragment = document.createDocumentFragment();
    entries.forEach(entry => {
      const button = document.createElement('button');
      button.className = 'projects-dir-item';
      button.type = 'button';
      button.dataset.path = entry.path || '';
      button.addEventListener('click', () => this._loadDirectoryPicker(button.dataset.path || ''));

      const name = document.createElement('span');
      name.className = 'projects-dir-name';
      name.textContent = entry.name || '';

      const sub = document.createElement('span');
      sub.className = 'projects-dir-sub';
      sub.textContent = entry.path || '';

      button.append(name, sub);
      fragment.appendChild(button);
    });
    container.replaceChildren(fragment);
  },

  _renderDirectoryEmpty(container, message) {
    const empty = document.createElement('div');
    empty.className = 'projects-dir-empty';
    empty.textContent = message;
    container.replaceChildren(empty);
  },

  _renderDirectoryBreadcrumb(container, path) {
    const parts = this._projectBreadcrumbs(path);
    if (!parts.length) {
      const empty = document.createElement('span');
      empty.className = 'projects-dir-empty-inline';
      empty.textContent = projectsT('projects.noPath', null, 'No path');
      container.replaceChildren(empty);
      return;
    }
    const fragment = document.createDocumentFragment();
    parts.forEach(part => {
      const button = document.createElement('button');
      button.className = 'projects-dir-crumb';
      button.type = 'button';
      button.dataset.path = part.path;
      button.textContent = part.label;
      button.addEventListener('click', () => this._loadDirectoryPicker(button.dataset.path || ''));
      fragment.appendChild(button);
    });
    container.replaceChildren(fragment);
  },

  _projectBreadcrumbs(path) {
    const raw = String(path || '').trim();
    if (!raw) return [];
    const windowsDrive = /^[A-Za-z]:[\\/]/.test(raw);
    const startsWithBackslashUNC = raw.startsWith('\\\\');
    const separator = startsWithBackslashUNC || (raw.includes('\\') && !raw.includes('/')) ? '\\' : '/';
    const tokens = raw.split(/[\\/]+/).filter(Boolean);
    if (!tokens.length) return [{ label: separator, path: separator }];

    if (/^[\\/]{2}/.test(raw) && tokens.length >= 2) {
      let current = separator + separator + tokens[0] + separator + tokens[1];
      const out = [{ label: current, path: current }];
      tokens.slice(2).forEach(token => {
        current += separator + token;
        out.push({ label: token, path: current });
      });
      return out;
    }

    if (windowsDrive) {
      let current = tokens[0] + separator;
      const out = [{ label: tokens[0], path: current }];
      tokens.slice(1).forEach(token => {
        current = current.endsWith(separator) ? current + token : current + separator + token;
        out.push({ label: token, path: current });
      });
      return out;
    }

    let current = separator;
    const out = [{ label: separator, path: separator }];
    tokens.forEach(token => {
      current = current === separator ? separator + token : current + separator + token;
      out.push({ label: token, path: current });
    });
    return out;
  },

  _suggestProjectFields(path) {
    if (!this._projectFieldsAuto) return;
    const keyInput = document.getElementById('projects-project-key');
    const nameInput = document.getElementById('projects-project-name');
    if (!keyInput || !nameInput) return;
    const parts = String(path || '').split(/[\\/]+/).filter(Boolean);
    const last = parts[parts.length - 1] || '';
    if (!last) return;
    nameInput.value = last;
    keyInput.value = last.replace(/[^A-Za-z0-9]+/g, '-').replace(/^-+|-+$/g, '').toUpperCase() || 'PROJECT';
  },

  _ticketsByStatus() {
    const grouped = {};
    for (const status of this._statuses) grouped[status.key] = [];
    for (const ticket of this._tickets) {
      const status = ticket.status || 'backlog';
      if (!grouped[status]) grouped[status] = [];
      grouped[status].push(ticket);
    }
    return grouped;
  },

  _selectedProjectObject() {
    return this._projects.find(project => this._projectKey(project) === this._selectedProject) || null;
  },

  _projectKey(project) {
    return project ? (project.key || project.id || '') : '';
  },

  _statusLabel(status) {
    return projectsT('projects.status.' + status.key, null, status.label);
  },

  _statusLabelForKey(key) {
    const status = this._statuses.find(item => item.key === key);
    return status ? this._statusLabel(status) : (key || '');
  },

  _field(form, name) {
    const field = form && form.elements ? form.elements[name] : null;
    return field && typeof field.value === 'string' ? field.value.trim() : '';
  },

  _setError(error) {
    this._error = error && error.message ? error.message : String(error || projectsT('projects.requestFailed', null, 'Request failed'));
  },
};
