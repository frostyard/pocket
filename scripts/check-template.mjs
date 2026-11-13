#!/usr/bin/env node
// Template gate (ADR-0003), run by `make verify`. Checks:
//   agent-symlinks  CLAUDE.md, GEMINI.md, .cursorrules and
//                   .github/copilot-instructions.md resolve to AGENTS.md;
//                   .claude/skills to .agents/skills (ADR-0002).
//   docs-index      every doc under docs/ (not TEMPLATE.md) is linked from
//                   docs/README.md, and every relative link there resolves.
//   risk-paths      .github/risk-paths.json passes core's validator at schema
//                   version 2 with a level in each entry, and rule C: the list
//                   and the caller are listed, both at L1 or L2. The caller
//                   runs frostyard/core's pr-triage.yml at a full commit SHA
//                   in the job pr-triage (frostyard/core docs/specs/risk-paths.md).
//   corrections     .memory/corrections/ passes core's record rules
//                   (frostyard/core ADR-0018, ADR-0081), via
//                   scripts/check-corrections.mjs.
//   vendored        the 2 files copied from frostyard/core match their sha256 pins.
import { createHash } from "node:crypto";
import { execFileSync } from "node:child_process";
import {
  existsSync,
  lstatSync,
  readFileSync,
  readdirSync,
  realpathSync,
  statSync,
} from "node:fs";
import { dirname, join, resolve, sep } from "node:path";
import { fileURLToPath } from "node:url";
import { checkRequiredPaths, parseStrictJson, validateRiskPaths } from "./lib/risk-paths.mjs";

const scriptDir = resolve(dirname(fileURLToPath(import.meta.url)));
const usage = "Usage: node scripts/check-template.mjs [--root DIR]";
let root = resolve(scriptDir, "..");
const args = process.argv.slice(2);
for (let i = 0; i < args.length; i++) {
  if (args[i] === "--root") {
    if (i + 1 >= args.length || !args[i + 1] || args[i + 1].startsWith("--")) {
      console.error(usage);
      process.exit(2);
    }
    root = resolve(args[++i]);
  } else {
    console.error(usage);
    process.exit(2);
  }
}

const failures = [];
const counts = { docs: 0, links: 0, riskEntries: 0, corrections: "" };

function failCheck(name, file, problem) {
  failures.push({ name, file, problem });
}

function sha256(file) {
  return createHash("sha256").update(readFileSync(file)).digest("hex");
}

// a. Canonical instruction and skill symlinks.
function checkAgentSymlinks() {
  const agents = join(root, "AGENTS.md");
  let agentsReal;
  try {
    const stat = lstatSync(agents);
    if (!stat.isFile() || stat.isSymbolicLink()) {
      failCheck("agent-symlinks", "AGENTS.md", "must be a regular file");
      return;
    }
    agentsReal = realpathSync(agents);
  } catch {
    failCheck("agent-symlinks", "AGENTS.md", "file does not exist");
    return;
  }

  const links = [
    ["CLAUDE.md", "AGENTS.md"],
    ["GEMINI.md", "AGENTS.md"],
    [".cursorrules", "AGENTS.md"],
    [".github/copilot-instructions.md", "AGENTS.md"],
  ];
  for (const [relative, _targetName] of links) {
    const file = join(root, relative);
    let stat;
    try {
      stat = lstatSync(file);
    } catch {
      failCheck("agent-symlinks", relative, "missing link");
      continue;
    }
    if (!stat.isSymbolicLink()) {
      failCheck("agent-symlinks", relative, "must be a symlink");
      continue;
    }
    try {
      if (realpathSync(file) !== agentsReal) {
        failCheck("agent-symlinks", relative, "symlink points to another file");
      }
    } catch {
      failCheck("agent-symlinks", relative, "broken link");
    }
  }

  const skillsLink = join(root, ".claude", "skills");
  const skillsDir = join(root, ".agents", "skills");
  try {
    if (!statSync(skillsDir).isDirectory()) {
      failCheck("agent-symlinks", ".agents/skills", "must be a directory");
    }
  } catch {
    failCheck("agent-symlinks", ".agents/skills", "directory does not exist");
  }

  let skillsStat;
  try {
    skillsStat = lstatSync(skillsLink);
  } catch {
    failCheck("agent-symlinks", ".claude/skills", "missing link");
  }
  if (skillsStat) {
    if (!skillsStat.isSymbolicLink()) {
      failCheck("agent-symlinks", ".claude/skills", "must be a symlink");
    } else {
      try {
        const skillReal = realpathSync(skillsDir);
        if (realpathSync(skillsLink) !== skillReal) {
          failCheck("agent-symlinks", ".claude/skills", "symlink points to another directory");
        }
      } catch {
        failCheck("agent-symlinks", ".claude/skills", "broken link");
      }
    }
  }
}

// b. Every Markdown document is indexed, and every docs index link resolves.
function markdownFiles(directory) {
  const found = [];
  if (!existsSync(directory)) return found;
  for (const entry of readdirSync(directory, { withFileTypes: true })) {
    const file = join(directory, entry.name);
    if (entry.isDirectory()) found.push(...markdownFiles(file));
    else if (entry.isFile() && entry.name.endsWith(".md")) found.push(file);
  }
  return found;
}

function checkDocsIndex() {
  const docsDir = join(root, "docs");
  const indexFile = join(docsDir, "README.md");
  let indexText;
  if (!existsSync(indexFile)) {
    failCheck("docs-index", "docs/README.md", "file does not exist");
    return;
  }
  indexText = readFileSync(indexFile, "utf8");

  const relativeLinks = new Set();

  function addLinkTarget(rawTarget) {
    let target = rawTarget;
    if (/^[A-Za-z][A-Za-z0-9+.-]*:/u.test(target) || target.startsWith("#")) return;
    target = target.split("#", 1)[0];
    if (!target) return;
    try {
      target = decodeURIComponent(target);
    } catch {
      failCheck("docs-index", "docs/README.md", `bad percent escape in link target ${rawTarget}`);
      return;
    }
    const resolved = resolve(docsDir, target);
    if (!existsSync(resolved)) {
      failCheck("docs-index", "docs/README.md", `link to missing path ${target}`);
    }
    relativeLinks.add(resolved);
    counts.links++;
  }

  const inlineLinkPattern = /\]\(\s*(?:<([^>\n]+)>|([^()\s]+))(?:\s+(?:"[^"\n]*"|'[^'\n]*'|\([^()\n]*\)))?\s*\)/g;
  for (const match of indexText.matchAll(inlineLinkPattern)) {
    addLinkTarget(match[1] ?? match[2]);
  }

  const referenceDefinitionPattern = /^ {0,3}\[[^\]\n]+\]:\s*(?:<([^>\n]+)>|(\S+))/gm;
  for (const match of indexText.matchAll(referenceDefinitionPattern)) {
    addLinkTarget(match[1] ?? match[2]);
  }

  for (const file of markdownFiles(docsDir)) {
    if (file === indexFile) continue;
    if (file.endsWith(`${sep}TEMPLATE.md`) || file.endsWith("/TEMPLATE.md")) continue;
    counts.docs++;
    if (!relativeLinks.has(file)) {
      const relative = file.slice(root.length + 1).split(sep).join("/");
      failCheck("docs-index", relative, "document is not linked from docs/README.md");
    }
  }
}

// c. Strict risk-path list at schema version 2, the caller pin, and rule C.
const CALLER_PATH = ".github/workflows/risk-paths.yml";
const CALLER_WORKFLOW = /^frostyard\/core\/\.github\/workflows\/pr-triage\.yml@[0-9a-f]{40}$/;
const CALLER_JOB = "pr-triage";

// The lines of a workflow that are not part of a block scalar: a line under a
// `key: |` or `key: >` scalar is text a step runs, never a workflow key. The
// caller structure is small enough to read line by line, so the gate needs no
// YAML dependency.
function structureLines(text) {
  const lines = [];
  let blockIndent = -1;
  for (const raw of text.split(/\r?\n/u)) {
    const indent = raw.length - raw.trimStart().length;
    const blank = raw.trim() === "";
    if (blockIndent >= 0) {
      if (blank || indent > blockIndent) {
        lines.push({ text: raw, indent, inBlock: true });
        continue;
      }
      blockIndent = -1;
    }
    lines.push({ text: raw, indent, inBlock: false });
    if (blank) continue;
    const scalar = /^\s*(?:-\s+)?[\w.-]+:[ \t]+([|>][-+0-9]*)[ \t]*(?:#.*)?$/u.exec(raw);
    if (scalar) blockIndent = indent;
  }
  return lines;
}

// The value of a `uses:` key line, without a trailing comment.
function usesValue(text) {
  return text.trim().replace(/^uses:[ \t]*/u, "").replace(/[ \t]+#.*$/u, "").trim();
}

// The caller file: core's reusable pr-triage workflow, as the `uses:` key at
// indent 4 of the job pr-triage (frostyard/core docs/specs/risk-paths.md, rule E).
// A uses: line in another job, or the same text inside a run: block, is not the
// caller and must not pass.
function checkCaller(caller) {
  let text;
  try {
    text = readFileSync(join(root, caller), "utf8");
  } catch {
    failCheck("risk-paths", caller, "caller file does not exist");
    return;
  }
  const lines = structureLines(text);
  const jobsAt = lines.findIndex((line) => !line.inBlock && line.indent === 0 && /^jobs:[ \t]*(?:#.*)?$/u.test(line.text));
  if (jobsAt < 0) {
    failCheck("risk-paths", caller, `has no top-level jobs: key, so it has no job ${CALLER_JOB}`);
    return;
  }
  const jobAt = lines.findIndex((line, index) => index > jobsAt && !line.inBlock
    && line.indent === 2 && /^[^\s#][^:]*:[ \t]*(?:#.*)?$/u.test(line.text.trimStart())
    && line.text.trim().replace(/:.*$/u, "") === CALLER_JOB);
  if (jobAt < 0) {
    failCheck("risk-paths", caller, `must run the reusable workflow in the job ${CALLER_JOB}`);
    return;
  }
  let uses = null;
  for (let index = jobAt + 1; index < lines.length; index++) {
    const line = lines[index];
    if (line.inBlock) continue;
    const stripped = line.text.trim();
    // A comment-only line never ends a YAML mapping, whatever its indent.
    if (stripped === "" || stripped.startsWith("#")) continue;
    if (line.indent <= 2) break;
    if (line.indent === 4 && /^uses:/u.test(stripped)) {
      uses = usesValue(stripped);
      break;
    }
  }
  if (uses === null) {
    failCheck(
      "risk-paths",
      caller,
      `the job ${CALLER_JOB} must set uses: at 4 spaces to frostyard/core/.github/workflows/pr-triage.yml at a full 40-character commit SHA; a uses: line in another job or in a run: block does not count`,
    );
    return;
  }
  if (!CALLER_WORKFLOW.test(uses)) {
    failCheck(
      "risk-paths",
      caller,
      `the uses: of the job ${CALLER_JOB} must be frostyard/core/.github/workflows/pr-triage.yml@<40-character commit SHA>, not ${JSON.stringify(uses)} (core ADR-0021)`,
    );
  }
}

function checkRiskPaths() {
  const relative = ".github/risk-paths.json";
  const absolute = join(root, relative);
  if (!existsSync(absolute)) {
    failCheck("risk-paths", relative, "file does not exist");
    return;
  }

  let list;
  try {
    list = parseStrictJson(readFileSync(absolute, "utf8"));
  } catch (error) {
    failCheck("risk-paths", relative, error.message);
    return;
  }

  const validation = validateRiskPaths(list);
  if (!validation.ok) {
    for (const error of validation.errors) {
      failCheck("risk-paths", relative, error);
    }
    return;
  }

  // A repository made from this template starts on list version 2, so a
  // version 1 list fails here. The validator accepts 1 and 2; for version 2 it
  // already requires default_level and the level of each entry.
  if (list.schema_version !== 2) {
    failCheck("risk-paths", relative, `schema_version: must be 2 in this template, not ${JSON.stringify(list.schema_version)}`);
  }

  checkCaller(CALLER_PATH);
  counts.riskEntries = list.paths.length;
  for (const error of checkRequiredPaths(list, [relative, CALLER_PATH])) {
    failCheck("risk-paths", relative, error);
  }
}

// d. Run the corrections child process from the same scripts folder.
function checkCorrections() {
  const script = join(scriptDir, "check-corrections.mjs");
  try {
    const output = execFileSync(process.execPath, [script, "--root", root], {
      cwd: root,
      encoding: "utf8",
      stdio: ["ignore", "pipe", "pipe"],
    }).trim();
    counts.corrections = output.replace(/^ok\s+corrections:\s*/, "");
  } catch (error) {
    const stdout = String(error.stdout || "").trim();
    const stderr = String(error.stderr || "").trim();
    const output = [stderr, stdout].filter(Boolean).join("\n");
    if (output) {
      for (const line of output.split("\n")) {
        if (line.startsWith("FAIL corrections:")) {
          failCheck("corrections", ".memory/corrections/", line.replace(/^FAIL corrections:\s*/, ""));
        } else {
          failCheck("corrections", ".memory/corrections/", line);
        }
      }
    } else {
      failCheck("corrections", ".memory/corrections/", `child process exited ${error.status ?? 1}`);
    }
  }
}

// e. Hash-pinned vendored core files. Update by copying core, never editing.
function checkVendored() {
  const vendored = [
    {
      path: "scripts/lib/risk-paths.mjs",
      source: "frostyard/core@c996ea3fb54bcf1a3c621113522213702a7673e2:scripts/lib/risk-paths.mjs",
      sha256: "6df0af009ff385467214f8df56aa5c5c92ac19ad26689b3344bb4d2654cdfa43",
    },
    {
      path: "scripts/check-corrections.mjs",
      source: "frostyard/core@c8962829f1f34d900e0632e63f690f81630702f1:scripts/check-corrections.mjs",
      sha256: "5c7a3a44a09d8c9d79efc919b42bb78b62b283616ffec16c211fe3e2b100008a",
    },
  ];
  for (const item of vendored) {
    const absolute = join(root, item.path);
    if (!existsSync(absolute) || !statSync(absolute).isFile()) {
      failCheck("vendored", item.path, `vendored from ${item.source}; copy it again from core and update the pin, never edit by hand`);
      continue;
    }
    if (sha256(absolute) !== item.sha256) {
      failCheck("vendored", item.path, `vendored from ${item.source}; copy it again from core and update the pin, never edit by hand`);
    }
  }
}

checkAgentSymlinks();
checkDocsIndex();
checkRiskPaths();
checkCorrections();
checkVendored();

if (failures.length > 0) {
  for (const failure of failures) {
    console.log(`FAIL ${failure.name}: ${failure.file}: ${failure.problem}`);
  }
  process.exit(1);
}

console.log("ok   agent-symlinks: 5 symlinks resolve to AGENTS.md and .agents/skills");
console.log(`ok   docs-index: ${counts.docs} docs indexed, ${counts.links} index links resolve`);
console.log(`ok   risk-paths: ${counts.riskEntries} entries at schema version 2, list and caller listed`);
console.log(`ok   corrections: ${counts.corrections}`);
console.log("ok   vendored: 2 pinned core copies match");
