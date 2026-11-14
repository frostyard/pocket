// The recorded GitHub answers (internal/github/testdata, ADR-0008) must stay
// sanitized: every JSON file parses, and no file holds a token, an email
// address or a response header.
import assert from "node:assert/strict";
import { test } from "node:test";
import { existsSync, readdirSync, readFileSync, statSync } from "node:fs";
import { dirname, join, relative, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const repo = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const testdata = join(repo, "internal", "github", "testdata");

// GitHub token forms (classic, OAuth, app, refresh, fine-grained), an email
// address, and the headers that `gh api -i` prints.
export const LEAKS = [
  ["GitHub token", /\b(?:gh[opsur]_[A-Za-z0-9]{20,}|github_pat_[A-Za-z0-9_]{20,})/],
  ["email address", /[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}/],
  ["response header", /^(?:HTTP\/[0-9.]+ [0-9]{3}|X-OAuth-Scopes:|X-GitHub-Request-Id:)/im],
];

export function leaks(text) {
  return LEAKS.filter(([, pattern]) => pattern.test(text)).map(([name]) => name);
}

function files(dir) {
  return readdirSync(dir).flatMap((name) => {
    const path = join(dir, name);
    return statSync(path).isDirectory() ? files(path) : [path];
  });
}

test("the recorded GitHub answers parse and hold no secret", () => {
  assert.ok(existsSync(testdata), "internal/github/testdata exists");
  const all = files(testdata);
  const json = all.filter((path) => path.endsWith(".json"));
  assert.ok(json.length > 0, "at least 1 recorded answer");
  for (const path of all) {
    const text = readFileSync(path, "utf8");
    assert.deepEqual(leaks(text), [], relative(repo, path));
    if (path.endsWith(".json")) assert.doesNotThrow(() => JSON.parse(text), relative(repo, path));
  }
});

test("the leak check finds each kind it names", () => {
  assert.deepEqual(leaks(`"token": "ghp_${"a".repeat(36)}"`), ["GitHub token"]);
  assert.deepEqual(leaks(`"token": "github_pat_${"B".repeat(30)}"`), ["GitHub token"]);
  assert.deepEqual(leaks(`"email": "someone@example.org"`), ["email address"]);
  assert.deepEqual(leaks("HTTP/2.0 200 OK\nX-Oauth-Scopes: repo"), ["response header"]);
  assert.deepEqual(leaks(`{"login": "bketelsen", "oid": "${"0".repeat(40)}"}`), []);
});
