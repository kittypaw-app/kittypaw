package core

import "strings"

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
var numberSchema = map[string]any{"type": "number"}

func objectSchema(required []string, properties map[string]any) map[string]any {
	schema := map[string]any{
		"type":                 "object",
		"properties":           properties,
		"additionalProperties": false,
	}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

func flexibleObjectSchema(required []string, properties map[string]any) map[string]any {
	schema := map[string]any{
		"type":                 "object",
		"properties":           properties,
		"additionalProperties": true,
	}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
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

func jsonValueSchema() map[string]any {
	return map[string]any{"description": "Any JSON value."}
}

func stringArraySchema() map[string]any {
	return map[string]any{"type": "array", "items": stringSchema}
}

func stringMapSchema() map[string]any {
	return map[string]any{"type": "object", "additionalProperties": stringSchema}
}

func enumStringSchema(values ...string) map[string]any {
	return map[string]any{"type": "string", "enum": values}
}

func httpOptionsSchema() map[string]any {
	return objectSchema(nil, map[string]any{"headers": stringMapSchema()})
}

func memoryKindSchema() map[string]any {
	return enumStringSchema("fact", "preference", "decision", "state", "ongoing_task", "open_question")
}

func memoryScopeSchema() map[string]any {
	return enumStringSchema("global", "conversation", "project", "channel")
}

func deliveryTargetSchema() map[string]any {
	return objectSchema(nil, map[string]any{
		"account_id":       stringSchema,
		"channel":          stringSchema,
		"chat_id":          stringSchema,
		"conversation_id":  stringSchema,
		"channel_user_id":  stringSchema,
		"reply_to_message": stringSchema,
	})
}

func fileSearchOptionsProperties() map[string]any {
	return map[string]any{
		"path":   stringSchema,
		"ext":    stringSchema,
		"limit":  integerSchema,
		"offset": integerSchema,
	}
}

func browserTargetOptionsSchema() map[string]any {
	return objectSchema(nil, map[string]any{"target_id": stringSchema, "targetId": stringSchema})
}

var curatedParameterSchemas = map[string]map[string]any{
	"Http.get":    objectSchema([]string{"url"}, map[string]any{"url": stringSchema, "options": httpOptionsSchema()}),
	"Http.post":   objectSchema([]string{"url", "body"}, map[string]any{"url": stringSchema, "body": jsonValueSchema(), "options": httpOptionsSchema()}),
	"Http.put":    objectSchema([]string{"url", "body"}, map[string]any{"url": stringSchema, "body": jsonValueSchema(), "options": httpOptionsSchema()}),
	"Http.delete": objectSchema([]string{"url"}, map[string]any{"url": stringSchema, "options": httpOptionsSchema()}),
	"Http.patch":  objectSchema([]string{"url", "body"}, map[string]any{"url": stringSchema, "body": jsonValueSchema(), "options": httpOptionsSchema()}),
	"Http.head":   objectSchema([]string{"url"}, map[string]any{"url": stringSchema, "options": httpOptionsSchema()}),

	"File.read":    objectSchema([]string{"path"}, map[string]any{"path": stringSchema}),
	"File.write":   objectSchema([]string{"path", "content"}, map[string]any{"path": stringSchema, "content": stringSchema}),
	"File.append":  objectSchema([]string{"path", "content"}, map[string]any{"path": stringSchema, "content": stringSchema}),
	"File.edit":    fileEditParametersSchema,
	"File.delete":  objectSchema([]string{"path"}, map[string]any{"path": stringSchema}),
	"File.list":    objectSchema([]string{"dir"}, map[string]any{"dir": stringSchema}),
	"File.exists":  objectSchema([]string{"path"}, map[string]any{"path": stringSchema}),
	"File.mkdir":   objectSchema([]string{"path"}, map[string]any{"path": stringSchema}),
	"File.search":  objectSchema([]string{"query"}, map[string]any{"query": stringSchema, "path": stringSchema, "ext": stringSchema, "limit": integerSchema, "offset": integerSchema, "options": objectSchema(nil, fileSearchOptionsProperties())}),
	"File.stats":   objectSchema(nil, map[string]any{"path": stringSchema}),
	"File.reindex": objectSchema(nil, map[string]any{"path": stringSchema}),
	"File.summary": objectSchema([]string{"path"}, map[string]any{"path": stringSchema, "model": stringSchema, "force_refresh": boolSchema, "options": objectSchema(nil, map[string]any{"model": stringSchema, "force_refresh": boolSchema})}),

	"Storage.get":    objectSchema([]string{"key"}, map[string]any{"key": stringSchema}),
	"Storage.set":    objectSchema([]string{"key", "value"}, map[string]any{"key": stringSchema, "value": jsonValueSchema()}),
	"Storage.delete": objectSchema([]string{"key"}, map[string]any{"key": stringSchema}),
	"Storage.list":   objectSchema(nil, map[string]any{}),

	"Notify.send":          objectSchema([]string{"text"}, map[string]any{"text": stringSchema, "target": deliveryTargetSchema()}),
	"Telegram.send":        objectSchema([]string{"text"}, map[string]any{"text": stringSchema}),
	"Telegram.sendMessage": objectSchema([]string{"text"}, map[string]any{"text": stringSchema}),
	"Slack.send":           objectSchema([]string{"text"}, map[string]any{"text": stringSchema}),
	"Discord.send":         objectSchema([]string{"text"}, map[string]any{"text": stringSchema}),

	"Gmail.list":   objectSchema(nil, map[string]any{"query": stringSchema, "limit": integerSchema, "options": objectSchema(nil, map[string]any{"query": stringSchema, "limit": integerSchema})}),
	"Gmail.search": objectSchema([]string{"query"}, map[string]any{"query": stringSchema, "limit": integerSchema, "options": objectSchema(nil, map[string]any{"limit": integerSchema})}),
	"Gmail.read":   objectSchema([]string{"id"}, map[string]any{"id": stringSchema}),

	"X.searchRecent": objectSchema([]string{"query"}, map[string]any{"query": stringSchema, "limit": integerSchema, "options": objectSchema(nil, map[string]any{"limit": integerSchema})}),
	"X.homeTimeline": objectSchema(nil, map[string]any{"limit": integerSchema, "options": objectSchema(nil, map[string]any{"limit": integerSchema})}),
	"X.user":         objectSchema([]string{"username"}, map[string]any{"username": stringSchema}),
	"X.userPosts":    objectSchema([]string{"username"}, map[string]any{"username": stringSchema, "limit": integerSchema, "options": objectSchema(nil, map[string]any{"limit": integerSchema})}),
	"X.post":         objectSchema([]string{"id_or_url"}, map[string]any{"id_or_url": stringSchema, "id": stringSchema, "url": stringSchema}),

	"Shell.exec": objectSchema([]string{"command"}, map[string]any{"command": stringSchema}),

	"Git.status": objectSchema(nil, map[string]any{}),
	"Git.log":    objectSchema(nil, map[string]any{"n": integerSchema}),
	"Git.diff":   objectSchema(nil, map[string]any{}),
	"Git.add":    objectSchema(nil, map[string]any{"path": stringSchema}),
	"Git.commit": objectSchema([]string{"message"}, map[string]any{"message": stringSchema}),
	"Git.push":   objectSchema(nil, map[string]any{}),
	"Git.pull":   objectSchema(nil, map[string]any{}),

	"Llm.generate": objectSchema([]string{"prompt"}, map[string]any{"prompt": stringSchema}),
	"Moa.query":    objectSchema([]string{"prompt"}, map[string]any{"prompt": stringSchema, "models": stringArraySchema(), "synthesizer": stringSchema, "per_model_timeout_ms": integerSchema, "options": objectSchema(nil, map[string]any{"models": stringArraySchema(), "synthesizer": stringSchema, "per_model_timeout_ms": integerSchema})}),
	"Code.exec":    objectSchema([]string{"js"}, map[string]any{"js": stringSchema}),

	"Memory.search": objectSchema([]string{"query"}, map[string]any{"query": stringSchema}),
	"Memory.set":    objectSchema([]string{"key", "value"}, map[string]any{"key": stringSchema, "value": stringSchema, "scope": memoryScopeSchema(), "kind": memoryKindSchema(), "confidence": numberSchema, "options": objectSchema(nil, map[string]any{"scope": memoryScopeSchema(), "kind": memoryKindSchema(), "confidence": numberSchema})}),
	"Memory.get":    objectSchema([]string{"key"}, map[string]any{"key": stringSchema}),
	"Memory.delete": objectSchema([]string{"key"}, map[string]any{"key": stringSchema}),
	"Memory.user":   objectSchema([]string{"key", "value"}, map[string]any{"key": stringSchema, "value": stringSchema, "scope": memoryScopeSchema(), "kind": memoryKindSchema(), "confidence": numberSchema, "options": objectSchema(nil, map[string]any{"scope": memoryScopeSchema(), "kind": memoryKindSchema(), "confidence": numberSchema})}),

	"Todo.list":   objectSchema(nil, map[string]any{}),
	"Todo.add":    objectSchema([]string{"text"}, map[string]any{"text": stringSchema}),
	"Todo.update": objectSchema([]string{"id", "text"}, map[string]any{"id": stringSchema, "text": stringSchema}),
	"Todo.delete": objectSchema([]string{"id"}, map[string]any{"id": stringSchema}),

	"Projects.list":             objectSchema(nil, map[string]any{}),
	"Projects.current":          objectSchema(nil, map[string]any{}),
	"Projects.show":             objectSchema([]string{"project"}, map[string]any{"project": stringSchema}),
	"Projects.listTickets":      objectSchema([]string{"project"}, map[string]any{"project": stringSchema}),
	"Projects.createTicket":     objectSchema([]string{"project", "title"}, map[string]any{"project": stringSchema, "title": stringSchema, "body": stringSchema, "status": stringSchema, "priority": integerSchema, "labels": stringArraySchema(), "created_by": stringSchema}),
	"Projects.showTicket":       objectSchema([]string{"ticket"}, map[string]any{"ticket": stringSchema}),
	"Projects.moveTicket":       objectSchema([]string{"ticket", "status"}, map[string]any{"ticket": stringSchema, "status": stringSchema, "actor_id": stringSchema, "message": stringSchema}),
	"Projects.commentTicket":    objectSchema([]string{"ticket", "body"}, map[string]any{"ticket": stringSchema, "author_id": stringSchema, "body": stringSchema}),
	"Projects.createBriefDraft": objectSchema([]string{"project", "title", "brief_json"}, map[string]any{"project": stringSchema, "title": stringSchema, "brief_json": stringSchema, "proposed_tickets_json": stringSchema, "created_by": stringSchema}),
	"Projects.updateBriefDraft": objectSchema([]string{"draft"}, map[string]any{"draft": stringSchema, "title": stringSchema, "brief_json": stringSchema, "proposed_tickets_json": stringSchema}),
	"Projects.commitBriefDraft": objectSchema([]string{"draft"}, map[string]any{"draft": stringSchema, "actor_id": stringSchema}),
	"Projects.planJob":          objectSchema([]string{"ticket"}, map[string]any{"ticket": stringSchema, "driver_id": stringSchema, "mode": stringSchema, "worktree_path": stringSchema, "branch_name": stringSchema, "prompt_summary": stringSchema, "prompt_text": stringSchema, "created_by": stringSchema}),
	"Projects.showJob":          objectSchema([]string{"job"}, map[string]any{"job": stringSchema}),
	"Projects.cancelJob":        objectSchema([]string{"job"}, map[string]any{"job": stringSchema, "actor_id": stringSchema, "reason": stringSchema}),
	"Projects.appendJobInput":   objectSchema([]string{"job", "text"}, map[string]any{"job": stringSchema, "actor_id": stringSchema, "text": stringSchema}),

	"Env.get": objectSchema([]string{"name"}, map[string]any{"name": stringSchema}),

	"Skill.list":                objectSchema(nil, map[string]any{}),
	"Skill.run":                 objectSchema([]string{"name"}, map[string]any{"name": stringSchema, "params": flexibleObjectSchema(nil, map[string]any{})}),
	"Skill.create":              objectSchema([]string{"name", "desc", "code"}, map[string]any{"name": stringSchema, "desc": stringSchema, "code": stringSchema, "triggerType": enumStringSchema("manual", "schedule", "once"), "schedule_or_run_at": stringSchema, "runOnInstall": boolSchema, "run_on_install": boolSchema}),
	"Skill.disable":             objectSchema([]string{"name"}, map[string]any{"name": stringSchema}),
	"Skill.uninstall":           objectSchema([]string{"name"}, map[string]any{"name": stringSchema}),
	"Skill.rollback":            objectSchema([]string{"name"}, map[string]any{"name": stringSchema}),
	"Skill.search":              objectSchema([]string{"query"}, map[string]any{"query": stringSchema}),
	"Skill.installFromRegistry": objectSchema([]string{"id"}, map[string]any{"id": stringSchema}),

	"Tts.speak":                objectSchema([]string{"text"}, map[string]any{"text": stringSchema}),
	"Image.generate":           objectSchema([]string{"prompt"}, map[string]any{"prompt": stringSchema}),
	"Vision.analyze":           objectSchema([]string{"imageUrl", "prompt"}, map[string]any{"imageUrl": stringSchema, "prompt": stringSchema}),
	"Vision.analyzeAttachment": objectSchema([]string{"attachmentId", "prompt"}, map[string]any{"attachmentId": stringSchema, "prompt": stringSchema}),

	"Mcp.call":      objectSchema([]string{"server", "tool", "args"}, map[string]any{"server": stringSchema, "tool": stringSchema, "args": flexibleObjectSchema(nil, map[string]any{})}),
	"Mcp.listTools": objectSchema([]string{"server"}, map[string]any{"server": stringSchema}),

	"Runner.delegate": objectSchema([]string{"staffId", "task"}, map[string]any{"staffId": stringSchema, "task": stringSchema}),
	"Runner.observe":  objectSchema([]string{"data"}, map[string]any{"data": jsonValueSchema(), "label": stringSchema}),

	"Staff.list":   objectSchema(nil, map[string]any{}),
	"Staff.switch": objectSchema([]string{"id"}, map[string]any{"id": stringSchema}),
	"Staff.create": objectSchema([]string{"id", "desc"}, map[string]any{"id": stringSchema, "desc": stringSchema}),
	"Staff.update": objectSchema([]string{"id", "desc"}, map[string]any{"id": stringSchema, "desc": stringSchema}),

	"Web.search": objectSchema([]string{"query"}, map[string]any{"query": stringSchema}),
	"Web.fetch":  objectSchema([]string{"url"}, map[string]any{"url": stringSchema}),

	"Browser.status":     objectSchema(nil, map[string]any{}),
	"Browser.open":       objectSchema(nil, map[string]any{"url": stringSchema}),
	"Browser.tabs":       objectSchema(nil, map[string]any{}),
	"Browser.use":        objectSchema([]string{"target_id"}, map[string]any{"target_id": stringSchema}),
	"Browser.navigate":   objectSchema([]string{"url"}, map[string]any{"url": stringSchema}),
	"Browser.snapshot":   objectSchema(nil, map[string]any{"target_id": stringSchema, "targetId": stringSchema, "options": browserTargetOptionsSchema()}),
	"Browser.click":      objectSchema([]string{"ref_or_selector"}, map[string]any{"ref_or_selector": stringSchema, "ref": stringSchema, "selector": stringSchema}),
	"Browser.type":       objectSchema([]string{"ref_or_selector", "text"}, map[string]any{"ref_or_selector": stringSchema, "ref": stringSchema, "selector": stringSchema, "text": stringSchema}),
	"Browser.evaluate":   objectSchema([]string{"js"}, map[string]any{"js": stringSchema}),
	"Browser.screenshot": objectSchema(nil, map[string]any{"format": enumStringSchema("png", "jpeg"), "options": objectSchema(nil, map[string]any{"format": enumStringSchema("png", "jpeg")})}),
	"Browser.close":      objectSchema(nil, map[string]any{"target_id": stringSchema}),

	"Share.read": objectSchema([]string{"account_id", "path"}, map[string]any{"account_id": stringSchema, "path": stringSchema}),

	"Fanout.send":      objectSchema([]string{"account_id", "text"}, map[string]any{"account_id": stringSchema, "text": stringSchema, "channel_hint": stringSchema}),
	"Fanout.broadcast": objectSchema([]string{"text"}, map[string]any{"text": stringSchema, "channel_hint": stringSchema}),
}

func curatedParameterSchema(skillName, methodName string) map[string]any {
	return curatedParameterSchemas[skillName+"."+methodName]
}

func withParameterSchemas(registry []SkillMeta) []SkillMeta {
	for i := range registry {
		for j := range registry[i].Methods {
			if schema := curatedParameterSchema(registry[i].Name, registry[i].Methods[j].Name); schema != nil {
				registry[i].Methods[j].ParametersSchema = schema
			} else if registry[i].Methods[j].ParametersSchema == nil {
				registry[i].Methods[j].ParametersSchema = parameterSchemaFromSignature(registry[i].Methods[j].Signature)
			}
		}
	}
	return registry
}

func parameterSchemaFromSignature(signature string) map[string]any {
	start := strings.IndexByte(signature, '(')
	end := strings.IndexByte(signature, ')')
	if start < 0 || end <= start+1 {
		return objectSchema(nil, map[string]any{})
	}
	inside := signature[start+1 : end]
	parts := splitSchemaSignatureArgs(inside)
	properties := map[string]any{}
	var required []string
	for _, part := range parts {
		raw := strings.TrimSpace(part)
		if raw == "" {
			continue
		}
		optional := strings.Contains(raw, "?") || strings.Contains(raw, "[") || strings.Contains(raw, "]")
		raw = strings.Trim(raw, "[]{}?")
		if idx := strings.IndexAny(raw, " :"); idx >= 0 {
			raw = raw[:idx]
		}
		name := strings.TrimSpace(raw)
		if name == "" {
			continue
		}
		properties[name] = schemaForParameterName(name)
		if !optional {
			required = append(required, name)
		}
	}
	return objectSchema(required, properties)
}

func splitSchemaSignatureArgs(inside string) []string {
	parts := strings.Split(inside, ",")
	if len(parts) == 1 && strings.TrimSpace(parts[0]) == "" {
		return nil
	}
	return parts
}

func schemaForParameterName(name string) map[string]any {
	switch name {
	case "options", "target", "params", "payload", "args":
		return flexibleObjectSchema(nil, map[string]any{})
	case "body", "value", "data":
		return map[string]any{"description": "Any JSON value."}
	case "priority", "limit", "offset", "n", "per_model_timeout_ms":
		return integerSchema
	case "labels", "models", "candidates":
		return map[string]any{"type": "array"}
	default:
		return stringSchema
	}
}

// SkillRegistry is the canonical list of all skill globals.
// Both the sandbox JS wrapper and the LLM system prompt are generated from
// this single source, preventing drift between what the sandbox provides
// and what the LLM is told is available.
var SkillRegistry = withParameterSchemas([]SkillMeta{
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
		{Name: "create", Signature: "Skill.create(name, desc, code, triggerType, scheduleOrRunAt, runOnInstall?) — for triggerType \"schedule\", pass cron/every; first run waits for the next scheduled time unless runOnInstall is true. For \"once\", pass a delay like \"2m\" or RFC3339 run_at."},
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
})
