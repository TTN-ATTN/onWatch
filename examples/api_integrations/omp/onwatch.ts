/**
 * onWatch usage reporter for omp.
 *
 * Install: copy this file to ~/.omp/agent/extensions/onwatch.ts and restart omp.
 *
 * For every model call omp makes (main agent, subagents, and side calls such as
 * titles and compaction) it appends one onWatch API Integrations event to
 * $ONWATCH_API_INTEGRATIONS_DIR/omp-YYYY-MM.jsonl (default
 * ~/.onwatch/api-integrations). onWatch tails that file, attaches each call to
 * the account it discovered from the same omp provider, and prices the tokens
 * with its own rates. Only provider, model, token counts and timing are
 * written: no prompts, replies, or keys. Set ONWATCH_OMP_USAGE=false to stop.
 */
import type { ExtensionAPI } from "@oh-my-pi/pi-coding-agent";
import { appendFileSync, mkdirSync } from "node:fs";
import { homedir } from "node:os";
import { join } from "node:path";

interface Call {
	provider?: string;
	model?: string;
	api?: string;
	usage: Record<string, unknown>;
	stopReason?: string;
	at?: number;
	requestId: string;
	latencyMs?: number;
	purpose: string;
	agent?: string;
}

// Module scope is shared by every session in this process (subagents included),
// so a call is reported once even if several sessions observe it.
const reported = new Set<string>();

// Session events and journal entries arrive as loosely typed records; every
// field read below is checked with str/num before use.
function record(value: unknown): Record<string, unknown> {
	return value !== null && typeof value === "object" ? (value as Record<string, unknown>) : {};
}
function str(value: unknown): string | undefined {
	return typeof value === "string" && value !== "" ? value : undefined;
}
function num(value: unknown): number | undefined {
	return typeof value === "number" && Number.isFinite(value) ? value : undefined;
}
function tokens(value: unknown): number {
	return Math.max(0, Math.floor(num(value) ?? 0));
}

function outputDir(): string {
	const configured = process.env.ONWATCH_API_INTEGRATIONS_DIR?.trim();
	if (!configured) return join(homedir(), ".onwatch", "api-integrations");
	return configured.startsWith("~/") ? join(homedir(), configured.slice(2)) : configured;
}

function report(pi: ExtensionAPI, call: Call): void {
	if (!call.provider || !call.model || reported.has(call.requestId)) return;
	const input = tokens(call.usage.input);
	const output = tokens(call.usage.output);
	const cacheRead = tokens(call.usage.cacheRead);
	const cacheWrite = tokens(call.usage.cacheWrite);
	// Failed or aborted calls report no tokens and cost nothing.
	if (input + output + cacheRead + cacheWrite === 0) return;
	reported.add(call.requestId);
	if (reported.size > 50_000) reported.clear(); // onWatch still drops replays by request_id

	// onWatch accepts lowercase provider slugs: a letter, then [a-z0-9._-].
	const slug = call.provider.toLowerCase().replace(/[^a-z0-9._-]+/g, "-").slice(0, 64);
	const at = new Date(call.at !== undefined && Number.isFinite(call.at) ? call.at : Date.now());
	const event: Record<string, unknown> = {
		ts: at.toISOString(),
		integration: "omp",
		provider: /^[a-z]/.test(slug) ? slug : `p-${slug}`.slice(0, 64),
		account: call.provider,
		omp_provider: call.provider,
		model: call.model,
		request_id: call.requestId,
		// omp counts input without the cached parts; onWatch's prompt total includes them.
		prompt_tokens: input + cacheRead + cacheWrite,
		completion_tokens: output,
		cache_read_tokens: cacheRead,
		cache_write_tokens: cacheWrite,
		total_tokens: input + output + cacheRead + cacheWrite,
		metadata: { purpose: call.purpose, agent: call.agent, api: call.api, stop_reason: call.stopReason },
	};
	if (call.latencyMs !== undefined && call.latencyMs >= 0) event.latency_ms = Math.round(call.latencyMs);
	const line = `${JSON.stringify(event)}\n`;
	const file = `omp-${at.toISOString().slice(0, 7)}.jsonl`;
	// Written synchronously: print-mode omp exits right after its reply, before a
	// queued async write would run. A short O_APPEND line is written whole, so
	// several omp processes can share the file.
	try {
		const dir = outputDir();
		mkdirSync(dir, { recursive: true, mode: 0o700 });
		appendFileSync(join(dir, file), line, { mode: 0o600 });
	} catch (error) {
		try {
			pi.logger.warn("onwatch: could not record model usage", { error: String(error) });
		} catch {
			// Never let usage reporting disturb the session.
		}
	}
}

type Ctx = { sessionManager?: unknown; agent?: unknown };

function sessionId(ctx: Ctx): string {
	try {
		const manager = record(ctx.sessionManager);
		return typeof manager.getSessionId === "function" ? String(manager.getSessionId.call(ctx.sessionManager)) : "session";
	} catch {
		return "session";
	}
}

function agentKind(ctx: Ctx): string | undefined {
	const agent = record(ctx.agent);
	return agent.kind === "sub" ? `sub:${str(agent.name) ?? "sub"}` : str(agent.kind);
}

function sideEntries(ctx: Ctx): Record<string, unknown>[] {
	try {
		const manager = record(ctx.sessionManager);
		if (typeof manager.getEntries !== "function") return [];
		const entries: unknown = manager.getEntries.call(ctx.sessionManager);
		return Array.isArray(entries) ? entries.map(record).filter(entry => entry.type === "model_usage") : [];
	} catch {
		return [];
	}
}

export default function onwatchUsage(pi: ExtensionAPI) {
	if (process.env.ONWATCH_OMP_USAGE?.trim().toLowerCase() === "false") return;
	pi.setLabel("onWatch usage");

	pi.on("message_end", (event, ctx) => {
		const message = record(record(event).message);
		if (message.role !== "assistant") return;
		const provider = str(message.provider);
		const model = str(message.model);
		const at = num(message.completedAt) ?? num(message.timestamp);
		report(pi, {
			provider,
			model,
			api: str(message.api),
			usage: record(message.usage),
			stopReason: str(message.stopReason),
			at,
			requestId: str(message.responseId) ?? `${sessionId(ctx)}:${provider}/${model}:${at}`,
			latencyMs: num(message.duration),
			purpose: "reply",
			agent: agentKind(ctx),
		});
	});

	// Side calls (titles, compaction, memory, ...) are journaled as model_usage
	// entries rather than replies. Calls already in a resumed session's history
	// were made earlier and are not reported again.
	pi.on("session_start", (_event, ctx) => {
		const id = sessionId(ctx);
		for (const entry of sideEntries(ctx)) if (str(entry.id)) reported.add(`${id}:${entry.id}`);
	});
	pi.on("agent_end", (_event, ctx) => {
		const id = sessionId(ctx);
		for (const entry of sideEntries(ctx)) {
			const entryId = str(entry.id);
			if (!entryId) continue;
			const timestamp = str(entry.timestamp);
			report(pi, {
				provider: str(entry.provider),
				model: str(entry.model),
				api: str(entry.api),
				usage: record(entry.usage),
				stopReason: str(entry.stopReason),
				at: timestamp ? Date.parse(timestamp) : undefined,
				requestId: `${id}:${entryId}`,
				latencyMs: num(entry.duration),
				purpose: str(entry.purpose) ?? "side-call",
				agent: agentKind(ctx),
			});
		}
	});
}
