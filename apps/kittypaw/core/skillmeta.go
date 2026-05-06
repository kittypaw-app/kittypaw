package core

// SkillMethodMeta describes one method on a skill global.
type SkillMethodMeta struct {
	Name      string // method name used in JS stubs: e.g. "get"
	Signature string // human-readable for LLM prompt: e.g. "Http.get(url)"
}

// SkillMeta describes a skill global available in the sandbox.
type SkillMeta struct {
	Name    string
	Methods []SkillMethodMeta
}

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
		{Name: "read", Signature: "File.read(path)"},
		{Name: "write", Signature: "File.write(path, content)"},
		{Name: "append", Signature: "File.append(path, content)"},
		{Name: "delete", Signature: "File.delete(path)"},
		{Name: "list", Signature: "File.list(dir)"},
		{Name: "exists", Signature: "File.exists(path)"},
		{Name: "mkdir", Signature: "File.mkdir(path)"},
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
	{Name: "Telegram", Methods: []SkillMethodMeta{
		{Name: "send", Signature: "Telegram.send(text)"},
		{Name: "sendMessage", Signature: "Telegram.sendMessage(text)"},
		{Name: "sendVoice", Signature: "Telegram.sendVoice(path)"},
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
		{Name: "set", Signature: "Memory.set(key, value)"},
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
	{Name: "Env", Methods: []SkillMethodMeta{
		{Name: "get", Signature: "Env.get(name)"},
	}},
	{Name: "Skill", Methods: []SkillMethodMeta{
		{Name: "list", Signature: "Skill.list()"},
		{Name: "run", Signature: "Skill.run(name[, params]) — execute an installed skill/package. Pass structured params declared by package.toml, e.g. Skill.run(\"weather-now\", {location:{label,lat,lon}})."},
		{Name: "create", Signature: "Skill.create(name, desc, code, triggerType, schedule)"},
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
	{Name: "Agent", Methods: []SkillMethodMeta{
		{Name: "delegate", Signature: "Agent.delegate(profileId, task) — delegates task to another agent"},
		{Name: "observe", Signature: "Agent.observe({data, label}) — pauses execution and sends data back for analysis. Engine re-calls LLM with observations in context."},
	}},
	{Name: "Profile", Methods: []SkillMethodMeta{
		{Name: "list", Signature: "Profile.list()"},
		{Name: "switch", Signature: "Profile.switch(id)"},
		{Name: "create", Signature: "Profile.create(id, desc)"},
		{Name: "update", Signature: "Profile.update(id, desc)"},
	}},
	{Name: "Web", Methods: []SkillMethodMeta{
		{Name: "search", Signature: "Web.search(query) — returns {results: [{title, url, snippet}]}"},
		{Name: "fetch", Signature: "Web.fetch(url) — returns {text, markdown, title, status}"},
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
