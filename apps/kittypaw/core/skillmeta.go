package core

// SkillMethodMeta describes one method on a skill global.
type SkillMethodMeta struct {
	Name             string         // method name used in JS stubs: e.g. "get"
	Signature        string         // human-readable for LLM prompt: e.g. "Http.get(url)"
	ParametersSchema map[string]any // JSON Schema for named/native tool arguments.
	ResultSchema     map[string]any // JSON Schema for structured tool results.
	Capabilities     []string       // product/audit flags such as filesystem_write.
}

const (
	CapabilityFilesystemRead  = "filesystem_read"
	CapabilityFilesystemWrite = "filesystem_write"
	CapabilityDestructive     = "destructive"
	CapabilityGuardedEdit     = "guarded_edit"
)

func (m SkillMethodMeta) HasCapability(capability string) bool {
	for _, got := range m.Capabilities {
		if got == capability {
			return true
		}
	}
	return false
}

// SkillMeta describes a skill global available in the sandbox.
type SkillMeta struct {
	Name    string
	Methods []SkillMethodMeta
}

var stringSchema = map[string]any{"type": "string"}
var boolSchema = map[string]any{"type": "boolean"}
var integerSchema = map[string]any{"type": "integer"}

func objectSchema(required []string, properties map[string]any) map[string]any {
	return map[string]any{
		"type":                 "object",
		"required":             required,
		"properties":           properties,
		"additionalProperties": false,
	}
}

var fileEditParametersSchema = objectSchema(
	[]string{"path", "old_text", "new_text"},
	map[string]any{
		"path":     stringSchema,
		"old_text": stringSchema,
		"new_text": stringSchema,
	},
)

var fileEditResultSchema = objectSchema(
	[]string{"success"},
	map[string]any{
		"success":      boolSchema,
		"replacements": integerSchema,
		"path":         stringSchema,
		"error":        stringSchema,
	},
)

// SkillRegistry is the canonical list of all skill globals.
// Both the sandbox JS wrapper and the LLM system prompt are generated from
// this single source, preventing drift between what the sandbox provides
// and what the LLM is told is available.
var SkillRegistry = []SkillMeta{
	{Name: "Http", Methods: []SkillMethodMeta{
		{Name: "get", Signature: "Http.get(url, options?) — options: {headers: {key: value}}"},
		{Name: "post", Signature: "Http.post(url, body, options?) — options: {headers: {key: value}}"},
		{Name: "put", Signature: "Http.put(url, body, options?) — options: {headers: {key: value}}"},
		{Name: "delete", Signature: "Http.delete(url, options?) — options: {headers: {key: value}}"},
		{Name: "patch", Signature: "Http.patch(url, body, options?) — options: {headers: {key: value}}"},
		{Name: "head", Signature: "Http.head(url, options?) — options: {headers: {key: value}}"},
	}},
	{Name: "File", Methods: []SkillMethodMeta{
		{Name: "read", Signature: "File.read(path)", Capabilities: []string{CapabilityFilesystemRead}},
		{Name: "write", Signature: "File.write(path, content)", Capabilities: []string{CapabilityFilesystemWrite}},
		{Name: "append", Signature: "File.append(path, content)", Capabilities: []string{CapabilityFilesystemWrite}},
		{Name: "edit", Signature: "File.edit(path, old_text, new_text) — guarded single replacement. Fails without changing the file unless old_text appears exactly once. Returns {success, replacements?, path?, error?}", ParametersSchema: fileEditParametersSchema, ResultSchema: fileEditResultSchema, Capabilities: []string{CapabilityFilesystemWrite, CapabilityGuardedEdit}},
		{Name: "delete", Signature: "File.delete(path)", Capabilities: []string{CapabilityFilesystemWrite, CapabilityDestructive}},
		{Name: "list", Signature: "File.list(dir)"},
		{Name: "exists", Signature: "File.exists(path)"},
		{Name: "mkdir", Signature: "File.mkdir(path)", Capabilities: []string{CapabilityFilesystemWrite}},
		{Name: "search", Signature: "File.search(query, options?) — search workspace files by keyword. options: {path, ext, limit, offset}"},
		{Name: "stats", Signature: "File.stats(path?) — workspace file statistics"},
		{Name: "reindex", Signature: "File.reindex(path?) — rebuild workspace file index"},
		{Name: "summary", Signature: "File.summary(path, options?) — LLM-generated file summary with cache. options: {model, force_refresh}. Returns {summary, model, cached, usage, content_hash}"},
	}},
	{Name: "Storage", Methods: []SkillMethodMeta{
		{Name: "get", Signature: "Storage.get(key)"},
		{Name: "set", Signature: "Storage.set(key, value)"},
		{Name: "delete", Signature: "Storage.delete(key)"},
		{Name: "list", Signature: "Storage.list()"},
	}},
	{Name: "Notify", Methods: []SkillMethodMeta{
		{Name: "send", Signature: "Notify.send(text, target?) — send a message to the current conversation or explicit target {channel, chat_id}"},
	}},
	{Name: "Telegram", Methods: []SkillMethodMeta{
		{Name: "send", Signature: "Telegram.send(text)"},
		{Name: "sendMessage", Signature: "Telegram.sendMessage(text)"},
	}},
	{Name: "Slack", Methods: []SkillMethodMeta{
		{Name: "send", Signature: "Slack.send(text)"},
	}},
	{Name: "Discord", Methods: []SkillMethodMeta{
		{Name: "send", Signature: "Discord.send(text)"},
	}},
	{Name: "Gmail", Methods: []SkillMethodMeta{
		{Name: "list", Signature: "Gmail.list(options?) — read-only Gmail recent messages. options: {query, limit}; default query: \"in:inbox category:primary\". Requires `kittypaw connect gmail`."},
		{Name: "search", Signature: "Gmail.search(queryOrOptions[, options]) — read-only Gmail search. Query examples: \"newer_than:1d\", \"from:alice@example.com\", \"subject:invoice\". options: {limit}."},
		{Name: "read", Signature: "Gmail.read(id) — read one Gmail message by ID and return headers + readable body text."},
	}},
	{Name: "X", Methods: []SkillMethodMeta{
		{Name: "searchRecent", Signature: "X.searchRecent(queryOrOptions[, options]) — read-only recent X posts search. options: {limit}; max 10. Requires `kittypaw connect x`. If result.error contains x_credits_depleted, X developer API credits are depleted; do not call it a connection/server problem."},
		{Name: "homeTimeline", Signature: "X.homeTimeline(options?) — read-only reverse chronological home timeline for the connected X account. options: {limit}; max 10. Not the For You/recommendation feed. If result.error contains x_credits_depleted, X developer API credits are depleted; do not call it a connection/server problem."},
		{Name: "user", Signature: "X.user(usernameOrOptions) — read one X user profile by @handle."},
		{Name: "userPosts", Signature: "X.userPosts(usernameOrOptions[, options]) — read recent posts from one X user. options: {limit}; max 10."},
		{Name: "post", Signature: "X.post(idOrUrl) — read one X post/tweet by status URL or ID."},
	}},
	{Name: "Shell", Methods: []SkillMethodMeta{
		{Name: "exec", Signature: "Shell.exec(command)"},
	}},
	{Name: "Git", Methods: []SkillMethodMeta{
		{Name: "status", Signature: "Git.status()"},
		{Name: "log", Signature: "Git.log(n)"},
		{Name: "diff", Signature: "Git.diff()"},
		{Name: "add", Signature: "Git.add(path)"},
		{Name: "commit", Signature: "Git.commit(msg)"},
		{Name: "push", Signature: "Git.push()"},
		{Name: "pull", Signature: "Git.pull()"},
	}},
	{Name: "Llm", Methods: []SkillMethodMeta{
		{Name: "generate", Signature: "Llm.generate(prompt) — returns {text, model, usage}"},
	}},
	{Name: "Moa", Methods: []SkillMethodMeta{
		{Name: "query", Signature: "Moa.query(prompt, options?) — parallel multi-model query + synthesis. options: {models, synthesizer, per_model_timeout_ms}. Returns {text, model, usage, candidates, synthesized}"},
	}},
	{Name: "Code", Methods: []SkillMethodMeta{
		{Name: "exec", Signature: "Code.exec(jsCode) — Run JS in an isolated pure-compute sandbox (no Http/Storage/Skill/Llm access, 1s timeout). Returns {result, logs} on success or {error, logs} on failure. Use for ad-hoc unit conversion, base reframe, scope filter, or any numeric transform you do not want to paraphrase from memory"},
	}},
	{Name: "Memory", Methods: []SkillMethodMeta{
		{Name: "search", Signature: "Memory.search(query)"},
		{Name: "set", Signature: `Memory.set(key, value, options?) - options.scope may be "conversation", "project", or "channel"; options.kind may be "fact", "preference", "decision", "state", "ongoing_task", or "open_question"`},
		{Name: "get", Signature: "Memory.get(key)"},
		{Name: "delete", Signature: "Memory.delete(key)"},
		{Name: "user", Signature: "Memory.user(key, value)"},
	}},
	{Name: "Todo", Methods: []SkillMethodMeta{
		{Name: "list", Signature: "Todo.list()"},
		{Name: "add", Signature: "Todo.add(text)"},
		{Name: "update", Signature: "Todo.update(id, text)"},
		{Name: "delete", Signature: "Todo.delete(id)"},
	}},
	{Name: "Projects", Methods: []SkillMethodMeta{
		{Name: "list", Signature: "Projects.list() — lists configured projects"},
		{Name: "current", Signature: "Projects.current() — returns the default/current project when one exists"},
		{Name: "show", Signature: "Projects.show(project) — returns {project, board}"},
		{Name: "listTickets", Signature: "Projects.listTickets(project) — lists tickets for a project"},
		{Name: "createTicket", Signature: "Projects.createTicket({project, title, body?, status?, priority?, labels?, created_by?}) — creates a ticket"},
		{Name: "showTicket", Signature: "Projects.showTicket(ticket) — returns {ticket, actions}"},
		{Name: "moveTicket", Signature: "Projects.moveTicket(ticket, {status, actor_id?, message?}) — records a ticket status change"},
		{Name: "commentTicket", Signature: "Projects.commentTicket(ticket, {author_id?, body}) — adds a ticket message"},
		{Name: "createBriefDraft", Signature: "Projects.createBriefDraft(project, {title, brief_json, proposed_tickets_json?, created_by?}) — creates a pending project brief draft"},
		{Name: "updateBriefDraft", Signature: "Projects.updateBriefDraft(draft, {title?, brief_json?, proposed_tickets_json?}) — edits a pending project brief draft"},
		{Name: "commitBriefDraft", Signature: "Projects.commitBriefDraft(draft, {actor_id?}) — commits an approved project brief draft"},
		{Name: "planJob", Signature: "Projects.planJob(ticket, {driver_id?, mode?, worktree_path?, branch_name?, prompt_summary?, prompt_text?, created_by?}) — creates a planned job record; does not start execution"},
		{Name: "showJob", Signature: "Projects.showJob(job) — returns a job record"},
		{Name: "cancelJob", Signature: "Projects.cancelJob(job, {actor_id?, reason?}) — cancels a planned job"},
		{Name: "appendJobInput", Signature: "Projects.appendJobInput(job, {actor_id?, text}) — records user input for a job"},
	}},
	{Name: "Env", Methods: []SkillMethodMeta{
		{Name: "get", Signature: "Env.get(name)"},
	}},
	{Name: "Skill", Methods: []SkillMethodMeta{
		{Name: "list", Signature: "Skill.list()"},
		{Name: "run", Signature: "Skill.run(name[, params]) — execute an installed skill/package. Pass structured params declared by package.toml, e.g. Skill.run(\"weather-now\", {location:{label,lat,lon}})."},
		{Name: "create", Signature: "Skill.create(name, desc, code, triggerType, scheduleOrRunAt) — for triggerType \"schedule\", pass cron/every; for \"once\", pass a delay like \"2m\" or RFC3339 run_at."},
		{Name: "disable", Signature: "Skill.disable(name)"},
		{Name: "uninstall", Signature: "Skill.uninstall(name) — remove an installed package or user-created skill by exact id/name. Destructive; call only when the user explicitly asks to remove/uninstall/delete it."},
		{Name: "rollback", Signature: "Skill.rollback(name)"},
		{Name: "search", Signature: "Skill.search(query) — search the public registry. Pass a keyword (e.g. \"환율\") to narrow to top 5 matches; pass \"\" to BROWSE (returns up to 30 — use when the user asks \"어떤 스킬들이 있나요?\"/\"what skills are available?\"/recommendations). Returns {results: [{id, name, version, description, author}], error?}."},
		{Name: "installFromRegistry", Signature: "Skill.installFromRegistry(id) — install a skill from the registry by id. CALL ONLY AFTER the user has explicitly agreed in chat (e.g. answered 네/yes/설치 to your suffix offer). Do NOT call this without prior consent."},
	}},
	{Name: "Tts", Methods: []SkillMethodMeta{
		{Name: "speak", Signature: "Tts.speak(text) — returns {path}"},
	}},
	{Name: "Image", Methods: []SkillMethodMeta{
		{Name: "generate", Signature: "Image.generate(prompt) — returns {url, imageUrl, model, error?}"},
	}},
	{Name: "Vision", Methods: []SkillMethodMeta{
		{Name: "analyze", Signature: "Vision.analyze(imageUrl, prompt) — returns {text}"},
		{Name: "analyzeAttachment", Signature: "Vision.analyzeAttachment(attachmentId, prompt) — analyze an image attached to the current user message; returns {text}"},
	}},
	{Name: "Mcp", Methods: []SkillMethodMeta{
		{Name: "call", Signature: "Mcp.call(server, tool, args) — calls an MCP tool"},
		{Name: "listTools", Signature: "Mcp.listTools(server) — lists tools on an MCP server"},
	}},
	{Name: "Runner", Methods: []SkillMethodMeta{
		{Name: "delegate", Signature: "Runner.delegate(staffId, task) — delegates task to another staff member"},
		{Name: "observe", Signature: "Runner.observe({data, label}) — pauses execution and sends data back for analysis. Engine re-calls LLM with observations in context."},
	}},
	{Name: "Staff", Methods: []SkillMethodMeta{
		{Name: "list", Signature: "Staff.list()"},
		{Name: "switch", Signature: "Staff.switch(id)"},
		{Name: "create", Signature: "Staff.create(id, desc) — creates a pending draft for approval; does not persist a staff until approved"},
		{Name: "update", Signature: "Staff.update(id, desc)"},
	}},
	{Name: "Web", Methods: []SkillMethodMeta{
		{Name: "search", Signature: "Web.search(query) — returns {results: [{title, url, snippet}]}"},
		{Name: "fetch", Signature: "Web.fetch(url) — returns {ok, error, text, markdown, title, status, contentType, finalUrl, backend}"},
	}},
	{Name: "Browser", Methods: []SkillMethodMeta{
		{Name: "status", Signature: "Browser.status() — returns managed Chrome status and diagnostics"},
		{Name: "open", Signature: "Browser.open(url?) — starts managed Chrome if needed, creates an active tab, and optionally navigates"},
		{Name: "tabs", Signature: "Browser.tabs() — lists controlled tabs"},
		{Name: "use", Signature: "Browser.use(targetId) — activates a controlled tab"},
		{Name: "navigate", Signature: "Browser.navigate(url) — navigates the active tab"},
		{Name: "snapshot", Signature: "Browser.snapshot(options?) — returns title, URL, visible text, and actionable element refs"},
		{Name: "click", Signature: "Browser.click(refOrSelector) — clicks an element ref from the latest snapshot or a CSS selector"},
		{Name: "type", Signature: "Browser.type(refOrSelector, text) — focuses an element and types text"},
		{Name: "evaluate", Signature: "Browser.evaluate(js) — runs bounded JavaScript in the active page and returns JSON-capped result"},
		{Name: "screenshot", Signature: "Browser.screenshot(options?) — saves a screenshot under the account data dir and returns {path, mime, bytes}"},
		{Name: "close", Signature: "Browser.close(targetId?) — closes a tab"},
	}},
	{Name: "Share", Methods: []SkillMethodMeta{
		{Name: "read", Signature: "Share.read(accountID, path) — read from a team-space account where you are a configured member. Paths must be memory/... or workspace/<alias>/...; returns {content}"},
	}},
	{Name: "Fanout", Methods: []SkillMethodMeta{
		{Name: "send", Signature: "Fanout.send(accountID, {text, channel_hint?}) — push a message to a configured team-space member; TEAM SPACE ONLY"},
		{Name: "broadcast", Signature: "Fanout.broadcast({text, channel_hint?}) — push to configured team-space members; TEAM SPACE ONLY"},
	}},
}
