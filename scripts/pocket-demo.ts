// A demo server for Pocket's development: a scripted model that thinks, runs tools, and asks, at a pace you can watch.
import { cleanUp, lastText, newSession, openApp, root, say } from "./helpers.ts";
import { createServer } from "node:http";
import type { FauxResponseStep } from "@earendil-works/pi-ai";
import { fauxAssistantMessage, fauxProvider, fauxText, fauxThinking, fauxToolCall } from "@earendil-works/pi-ai/providers/faux";
import { createHandler } from "../src/server/http.ts";
import { BACKGROUND_CONTEXT } from "@earendil-works/chord/context";

const port = Number(process.argv[2] ?? 8898);
const host = process.argv[3] ?? "127.0.0.3";

const route: FauxResponseStep = (request) => {
    const messages = (request as { messages: { role: string; content: unknown }[] }).messages;
    const { role, text } = lastText(request as never);
    let firstUser = 0;
    for (let i = messages.length - 1; i >= 0; i--) {
        if (messages[i]!.role === "user") { firstUser = i; break; }
    }
    const results = messages.slice(firstUser).filter((m) => m.role === "toolResult").length;
    const userText = lastText({ messages: messages.slice(0, firstUser + 1) } as never).text;
    const call = (parts: Parameters<typeof fauxAssistantMessage>[0]) => fauxAssistantMessage(parts, { stopReason: "toolUse" });

    if (/fix|bug/i.test(userText)) {
        if (results === 0) {
            return call([
                fauxThinking("The redirect loop probably comes from the session cookie being set on the wrong path. Let me look at the auth code first."),
                fauxText("I'll start by looking at how sign-in sets the cookie."),
                fauxToolCall("bash", { command: "ls -la && git log --oneline -5" }),
            ]);
        }
        if (results === 1) {
            return call([fauxToolCall("read", { path: "src/auth.ts" }), fauxToolCall("bash", { command: "grep -rn 'Set-Cookie' . | head" })]);
        }
        return fauxAssistantMessage([fauxText(
            "Found it. The cookie was scoped to `/login`, so every request after sign-in arrived **without** it and bounced back.\n\n" +
            "### What I changed\n\n1. Scoped the cookie to `/`\n2. Added a test that follows the redirect\n3. Kept `SameSite=Lax` so links from mail still sign in\n\n" +
            "```ts\nres.setHeader(\"Set-Cookie\", `auth=${token}; Path=/; HttpOnly; SameSite=Lax`);\n```\n\n" +
            "| Check | Before | After |\n|---|---|---|\n| Sign-in | loops | lands on `/` |\n| Tests | 47 | 48 passing |\n\nWant me to open a PR?")]);
    }
    if (/ask me|choose|decide/i.test(userText)) {
        if (results === 0) {
            return call([fauxText("Before I build the cache, one decision is yours."), fauxToolCall("ask", { questions: [
                { question: "Which store should back the cache?", header: "Store", options: [
                    { label: "Postgres (Recommended)", description: "Already deployed; one less moving part" },
                    { label: "Redis", description: "Fastest, but a new service to run" },
                    { label: "SQLite", description: "Local only; fine for a single node" },
                ] },
            ] })]);
        }
        return fauxAssistantMessage([fauxText(`Great, going with that. ${text.slice(0, 200)}`)]);
    }
    if (role === "toolResult") {
        return fauxAssistantMessage([fauxText("Done.")]);
    }
    return fauxAssistantMessage([fauxThinking("A simple echo will do."), fauxText(`echo: ${text}`)]);
};

const faux = fauxProvider({ tokensPerSecond: 90, models: [{ id: "faux-1" }, { id: "faux-2" }] });
faux.setResponses(Array.from({ length: 1000 }, () => route));
const app = await openApp(faux);
const server = createServer(createHandler({ app, listen: { host, port }, restart: () => {} }));
await new Promise<void>((resolve) => server.listen(port, host, resolve));

const first = await newSession(app);
await say(app, first, "Hello! Show me what you can do.");
const deploy = await newSession(app);
await say(app, deploy, "Deploy the staging build");
// A call that waits for approval, as Lancet Guard asks.
void app.approvals.request({
    id: "approval-demo-1", conversationId: deploy, taskId: 1 as never, tool: "bash",
    subject: "npm run deploy -- --env staging", reason: "Deploys change shared infrastructure.", createdAt: Date.now(),
}, BACKGROUND_CONTEXT).then((a) => console.log("approval answered", JSON.stringify(a)));

const base = `http://${host}:${port}`;
console.log(`Pocket demo server, data in ${root}`);
console.log(`  owner: ${base}/login?token=${encodeURIComponent(app.config.ownerToken)}`);
const stop = async () => { server.closeAllConnections(); server.close(); await app.close(); cleanUp(); process.exit(0); };
process.on("SIGINT", () => void stop());
process.on("SIGTERM", () => void stop());
