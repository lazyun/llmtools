// llmTools runtime control tools for the experimental Pi backend spike.
//
// This file is embedded in the llmTools binary and materialised to a private
// path at run time; Pi loads it with `pi -e`. It is a transport bridge only:
// every repository read is executed by the llmTools control entrypoint, which
// owns workspace authorization, and every suspension is reported back to the
// llmTools adapter instead of being answered by the model.
import { Type } from "typebox";
import { spawnSync, spawn } from "node:child_process";

const CONTROL_BIN = process.env.LLMTOOLS_CONTROL_BIN || "";
const CONTROL_DB = process.env.LLMTOOLS_CONTROL_DB || "";
const CONTROL_TOKEN = process.env.LLMTOOLS_CONTROL_TOKEN || "";
const REPO_READ = process.env.LLMTOOLS_REPO_READ === "1";
const REPO_WRITE = process.env.LLMTOOLS_REPO_WRITE === "1";
const SHELL = process.env.LLMTOOLS_SHELL === "1";

const REPO_TOOL_NAMES = [
  "llmtools_control_repo_read",
  "llmtools_control_repo_glob",
  "llmtools_control_repo_grep",
];
const CONTROL_TOOL_NAMES = [
  "llmtools_control_request_context",
  "llmtools_control_request_decision",
];
const ACTIVE = new Set([
  ...(REPO_READ ? REPO_TOOL_NAMES : []),
  ...CONTROL_TOOL_NAMES,
  ...(REPO_WRITE ? ["llmtools_control_repo_write", "llmtools_control_repo_edit"] : []),
  ...(SHELL ? ["llmtools_control_repo_exec"] : []),
]);

function gatewayTool(name) {
  return name.replace(/^llmtools_control_/, "");
}

function log(line) {
  process.stderr.write(`LLMTOOLS_PI ${line}\n`);
}

// The control token is never taken from the model. Pi sessions can resume
// across processes, so the only authority a tool call can carry is the one the
// adapter injected for this run.
function withToken(input) {
  return { ...(input && typeof input === "object" ? input : {}), control_token: CONTROL_TOKEN };
}

function callGateway(tool, input) {
  if (!CONTROL_BIN || !CONTROL_DB) throw new Error("CONTEXT_TOOL_UNAVAILABLE");
  const payload = JSON.stringify(withToken(input));
  const proc = spawnSync(CONTROL_BIN, ["--llmtools-pi-control", CONTROL_DB, tool], {
    input: payload,
    encoding: "utf8",
    maxBuffer: 8 * 1024 * 1024,
    timeout: 120000,
    env: { ...process.env, LLMTOOLS_CONTROL_TOKEN: CONTROL_TOKEN },
  });
  if (proc.error) throw new Error(proc.error.message);
  // The gateway writes the operator's repository audit to its own stderr. Pi
  // captures an extension's stderr, so forwarding each line keeps that evidence
  // on the one stream the worker already collects. Audit lines carry run and
  // workspace identity plus the navigation target, never content or tokens.
  const lines = String(proc.stderr || "").split("\n").filter((l) => l.length > 0);
  for (const line of lines) process.stderr.write(line.endsWith("\n") ? line : `${line}\n`);
  const out = (proc.stdout || "").trim();
  if (proc.status !== 0) {
    throw new Error((lines[lines.length - 1] || "INTERNAL_ERROR").slice(0, 200));
  }
  return out || "{}";
}

const tokenParam = Type.Optional(
  Type.String({ description: "Ignored by llmTools; the run's control token is injected by the runtime bridge." }),
);

function repoTools(pi) {
  pi.registerTool({
    name: "llmtools_control_repo_read",
    label: "llmTools repo read",
    description:
      "Read one file from the workspace authorized for this run. Paths may be workspace-relative or absolute inside the workspace. Read-only: outside paths, traversal, and symlink escapes are denied by the server.",
    promptSnippet: "Read one file inside the workspace authorized for this run",
    parameters: Type.Object({ control_token: tokenParam, path: Type.String({ description: "Workspace-relative file path." }) }),
    async execute(_id, input) {
      return { content: [{ type: "text", text: callGateway(gatewayTool("llmtools_control_repo_read"), input) }], details: {} };
    },
  });
  pi.registerTool({
    name: "llmtools_control_repo_glob",
    label: "llmTools repo glob",
    description:
      'List files in the workspace authorized for this run by glob pattern, for example "src/**" or "**/*.go". Read-only and confined: results never include paths outside the workspace or reached through an escaping symlink.',
    promptSnippet: "List files inside the authorized workspace by glob",
    parameters: Type.Object({
      control_token: tokenParam,
      pattern: Type.String({ description: "Glob pattern." }),
      path: Type.Optional(Type.String({ description: "Optional directory inside the workspace." })),
    }),
    async execute(_id, input) {
      return { content: [{ type: "text", text: callGateway(gatewayTool("llmtools_control_repo_glob"), input) }], details: {} };
    },
  });
  pi.registerTool({
    name: "llmtools_control_repo_grep",
    label: "llmTools repo grep",
    description:
      "Search file contents in the workspace authorized for this run with a regular expression, optionally narrowed by a glob or a path. Read-only: matches outside the workspace are never returned.",
    promptSnippet: "Search file contents inside the authorized workspace",
    parameters: Type.Object({
      control_token: tokenParam,
      pattern: Type.String({ description: "Regular expression." }),
      glob: Type.Optional(Type.String({ description: "Optional glob filter." })),
      path: Type.Optional(Type.String({ description: "Optional path inside the workspace." })),
    }),
    async execute(_id, input) {
      return { content: [{ type: "text", text: callGateway(gatewayTool("llmtools_control_repo_grep"), input) }], details: {} };
    },
  });
}

function callCommandGateway(input, signal, onUpdate) {
  if (!CONTROL_BIN || !CONTROL_DB) return Promise.reject(new Error("CONTEXT_TOOL_UNAVAILABLE"));
  return new Promise((resolve, reject) => {
    const child = spawn(CONTROL_BIN, ["--llmtools-pi-control", CONTROL_DB, "repo_exec"], {
      stdio: ["pipe", "pipe", "pipe"],
      env: { ...process.env, LLMTOOLS_CONTROL_TOKEN: CONTROL_TOKEN },
    });
    let stdout = "", stderr = "", killTimer;
    const abort = () => {
      child.kill("SIGTERM");
      killTimer = setTimeout(() => child.kill("SIGKILL"), 1000);
    };
    const deadline = setTimeout(abort, 3605000);
    const heartbeat = setInterval(() => {
      if (typeof onUpdate === "function") onUpdate({ content: [{ type: "text", text: "Project command running…" }], details: {} });
    }, 5000);
    const cleanup = () => {
      clearTimeout(deadline); clearInterval(heartbeat); clearTimeout(killTimer);
      signal?.removeEventListener("abort", abort);
    };
    signal?.addEventListener("abort", abort, { once: true });
    child.stdout.on("data", (chunk) => { stdout += String(chunk); if (stdout.length > 8 * 1024 * 1024) abort(); });
    child.stderr.on("data", (chunk) => { stderr += String(chunk); if (stderr.length > 1024 * 1024) abort(); });
    child.once("error", (error) => { cleanup(); reject(error); });
    child.once("close", (code) => {
      cleanup();
      for (const line of stderr.split("\n").filter(Boolean)) process.stderr.write(line + "\n");
      if (signal?.aborted) return reject(new Error("Command cancelled"));
      if (code !== 0) return reject(new Error((stderr.trim().split("\n").pop() || "INTERNAL_ERROR").slice(0, 200)));
      resolve(stdout || "{}");
    });
    child.stdin.on("error", () => {});
    child.stdin.end(JSON.stringify(withToken(input)));
    if (signal?.aborted) abort();
  });
}

function executionTools(pi) {
  if (REPO_WRITE) {
    pi.registerTool({
      name: "llmtools_control_repo_write", label: "llmTools project write",
      description: "Create or replace a UTF-8 file inside this run's authorized workspace. Parent directories are created. Outside paths, escaping symlinks and root .git metadata are denied.",
      parameters: Type.Object({ path: Type.String(), content: Type.String() }),
      async execute(_id, input) {
        return { content: [{ type: "text", text: callGateway("repo_write", input) }], details: {} };
      },
    });
    pi.registerTool({
      name: "llmtools_control_repo_edit", label: "llmTools project edit",
      description: "Replace exactly one occurrence of old_text with new_text in a workspace file. Zero or multiple matches fail; read the current file before editing.",
      parameters: Type.Object({ path: Type.String(), old_text: Type.String(), new_text: Type.String() }),
      async execute(_id, input) {
        return { content: [{ type: "text", text: callGateway("repo_edit", input) }], details: {} };
      },
    });
  }
  if (SHELL) {
    pi.registerTool({
      name: "llmtools_control_repo_exec", label: "llmTools project command",
      description: "Run a foreground implementation or test command in the project through the macOS sandbox. Writes are limited to the workspace and private run scratch; root .git writes and outbound network access (including localhost) are denied. Standard toolchains/dependency caches are readable. Provider keys are not inherited. Inspect exit_code, stdout, stderr, timed_out and truncated. Report actual test results to parent Codex for acceptance.",
      parameters: Type.Object({ command: Type.String(), timeout_seconds: Type.Optional(Type.Integer({ minimum: 1, maximum: 3600, description: "Defaults to 120 seconds." })) }),
      async execute(_id, input, signal, onUpdate) {
        return { content: [{ type: "text", text: await callCommandGateway(input, signal, onUpdate) }], details: {} };
      },
    });
  }
}

function controlTools(pi) {
  pi.registerTool({
    name: "llmtools_control_request_context",
    label: "llmTools request context",
    description:
      "Suspend this execution and request missing facts, files, symbols, or contracts from the parent Codex agent. Call it on its own; do not batch it with other tools and do not ask for context in final text.",
    promptSnippet: "Request missing context from the parent agent and suspend this run",
    promptGuidelines: [
      "Use llmtools_control_request_context instead of guessing missing facts, and call it as the only tool in the batch.",
    ],
    parameters: Type.Object({
      control_token: tokenParam,
      requests: Type.Array(
        Type.Object({
          id: Type.String({ description: "Unique request id for this session." }),
          kind: Type.String({ description: "file | symbol | contract | other." }),
          target: Type.String({ description: "What is needed." }),
          reason: Type.String({ description: "Why the execution cannot continue without it." }),
        }),
        { minItems: 1, maxItems: 20 },
      ),
    }),
    async execute(_id, input) {
      const requests = Array.isArray(input?.requests) ? input.requests : [];
      log(`control context ${JSON.stringify(requests.map((r) => r && r.id))}`);
      return {
        content: [{ type: "text", text: "Context requested. This execution is suspended until the parent agent answers." }],
        details: { llmtools_control: "context", requests },
        terminate: true,
      };
    },
  });
  pi.registerTool({
    name: "llmtools_control_request_decision",
    label: "llmTools request decision",
    description:
      "Suspend this execution and ask the parent Codex agent for a project-level decision it owns. Call it on its own; do not batch it with other tools and do not choose for the parent.",
    promptSnippet: "Request a parent-owned decision and suspend this run",
    promptGuidelines: [
      "Use llmtools_control_request_decision instead of choosing project-level semantics yourself, and call it as the only tool in the batch.",
    ],
    parameters: Type.Object({
      control_token: tokenParam,
      questions: Type.Array(
        Type.Object({
          id: Type.String({ description: "Unique request id for this session." }),
          question: Type.String({ description: "The decision the parent owns." }),
          header: Type.Optional(Type.String({ description: "Short label." })),
          options: Type.Optional(
            Type.Array(
              Type.Object({
                label: Type.String({ description: "Option label." }),
                description: Type.Optional(Type.String({ description: "What the option implies." })),
              }),
              { maxItems: 100 },
            ),
          ),
          multiple: Type.Optional(Type.Boolean({ description: "Allow selecting several options." })),
          allow_custom: Type.Optional(Type.Boolean({ description: "Allow a free-text answer." })),
        }),
        { minItems: 1, maxItems: 20 },
      ),
    }),
    async execute(_id, input) {
      const questions = Array.isArray(input?.questions) ? input.questions : [];
      log(`control decision ${JSON.stringify(questions.map((q) => q && q.id))}`);
      return {
        content: [{ type: "text", text: "Decision requested. This execution is suspended until the parent agent answers." }],
        details: { llmtools_control: "decision", questions },
        terminate: true,
      };
    },
  });
}

export default function (pi) {
  // Pi is started with --no-builtin-tools plus an exact --tools allowlist, so a
  // native execution tool reaching this hook means the surface moved. Deny it
  // here as well rather than trusting the launch flags alone.
  pi.on("tool_call", async (event) => {
    if (ACTIVE.has(event.toolName)) return undefined;
    log(`blocked ${event.toolName}`);
    return { block: true, reason: "llmTools runtime policy: this tool is not authorized for this run" };
  });

  // A suspension must end the agent loop even if the model batched the control
  // call with other tools, which is the only case terminate:true does not cover.
  pi.on("tool_result", async (event, ctx) => {
    const details = event?.details;
    if (!details || !String(details.llmtools_control || "").length) return undefined;
    try {
      if (typeof ctx?.abort === "function") ctx.abort();
    } catch (error) {
      log(`abort-after-control-failed ${error && error.message}`);
    }
    return undefined;
  });

  if (REPO_READ) repoTools(pi);
  executionTools(pi);
  controlTools(pi);
  log(`loaded repo_read=${REPO_READ} tools=${JSON.stringify([...ACTIVE])}`);
}
