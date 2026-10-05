// Tests for npm/publish.sh, which the release workflow runs to put a release on
// npm. Run with: node --test npm/test/
//
// Each test runs the real script with fake-npm.cjs first on PATH as `npm`. The
// fake keeps a registry in a JSON file: what is live, who published it, and
// which packages accept the run as a trusted publisher. The assertions read
// that file, so what is under test is what the script would have done to npm.

import { describe, test } from "node:test";
import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import {
  chmodSync, copyFileSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync,
} from "node:fs";
import { tmpdir } from "node:os";
import { delimiter, dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));
const script = join(here, "..", "publish.sh");
const skip = process.platform === "win32" && "publish.sh is a bash script";

const platformPackages = [
  "@intuizi/cli-darwin-arm64",
  "@intuizi/cli-darwin-x64",
  "@intuizi/cli-linux-arm64",
  "@intuizi/cli-linux-x64",
  "@intuizi/cli-win32-arm64",
  "@intuizi/cli-win32-x64",
];
const everyPackage = [...platformPackages, "@intuizi/cli"];

// What a workflow run looks like to npm when its job holds the id-token
// permission. Without these npm attempts no OIDC exchange at all.
const workflowRun = {
  GITHUB_ACTIONS: "true",
  ACTIONS_ID_TOKEN_REQUEST_URL: "https://actions.invalid/idtoken",
  ACTIONS_ID_TOKEN_REQUEST_TOKEN: "request-token",
};

// publish lays out what npm/build.mjs writes for one version, next to a fake
// registry in the given state, runs the script against the two and reports
// what happened: to the registry and on the console. Every package trusts the
// run unless the state says otherwise.
async function publish(t, version, state = {}, { args, idToken = true } = {}) {
  const root = mkdtempSync(join(tmpdir(), "intuizi-publish-"));
  t.after(() => rmSync(root, { recursive: true, force: true }));

  mkdirSync(join(root, "bin"));
  copyFileSync(join(here, "fake-npm.cjs"), join(root, "bin", "npm"));
  chmodSync(join(root, "bin", "npm"), 0o755);

  const packages = join(root, "packages");
  for (const name of everyPackage) {
    mkdirSync(join(packages, name), { recursive: true });
    writeFileSync(join(packages, name, "package.json"), JSON.stringify({ name, version }));
  }

  const registry = join(root, "registry.json");
  writeFileSync(registry, JSON.stringify({
    trusted: everyPackage, live: [], broken: [], login: false, ...state,
  }));

  // Start from an environment with no trace of a workflow run, so the result
  // is the same on a laptop and in CI.
  const env = { ...process.env, FAKE_NPM_REGISTRY: registry };
  for (const name of Object.keys(workflowRun)) delete env[name];
  env.PATH = join(root, "bin") + delimiter + env.PATH;
  if (idToken) Object.assign(env, workflowRun);

  const run = await new Promise((resolve, reject) => {
    const proc = spawn(script, args ?? [packages], { stdio: ["ignore", "pipe", "pipe"], env });
    let stdout = "";
    let stderr = "";
    proc.stdout.on("data", (chunk) => { stdout += chunk; });
    proc.stderr.on("data", (chunk) => { stderr += chunk; });
    proc.on("error", reject);
    proc.on("close", (status) => resolve({ status, output: stdout + stderr }));
  });

  const after = JSON.parse(readFileSync(registry, "utf8"));
  return {
    ...run,
    live: after.live.map((p) => `${p.name}@${p.version}`),
    registry: after,
    // The packages the run flagged as refused, with what it said about each.
    refused: Object.fromEntries(
      [...run.output.matchAll(/^::error title=[^:]+::(@intuizi\/[a-z0-9-]+): (.*)$/gm)].map((m) => [m[1], m[2]]),
    ),
  };
}

describe("npm/publish.sh", { skip, concurrency: true }, () => {
  // ------------------------------------------------------------ publishing

  test("publishes every package when the registry accepts the run as a trusted publisher of each", async (t) => {
    const run = await publish(t, "0.1.9");

    assert.equal(run.status, 0, run.output);
    assert.deepEqual([...run.live].sort(), [
      "@intuizi/cli-darwin-arm64@0.1.9",
      "@intuizi/cli-darwin-x64@0.1.9",
      "@intuizi/cli-linux-arm64@0.1.9",
      "@intuizi/cli-linux-x64@0.1.9",
      "@intuizi/cli-win32-arm64@0.1.9",
      "@intuizi/cli-win32-x64@0.1.9",
      "@intuizi/cli@0.1.9",
    ]);
  });

  // The launcher pins every platform package at the same version, so it must
  // be the last to appear: until then nothing can install a half-there release.
  test("publishes the launcher after the platform packages", async (t) => {
    const run = await publish(t, "0.1.9");

    assert.equal(run.live.at(-1), "@intuizi/cli@0.1.9", run.output);
  });

  // What an upload must carry: public access, because a scoped package is
  // private by default, and provenance, which ties the tarball to this run.
  test("publishes with public access and provenance", async (t) => {
    const run = await publish(t, "0.1.9");

    assert.deepEqual(
      run.registry.live.map(({ access, provenance }) => ({ access, provenance })),
      Array(7).fill({ access: "public", provenance: true }),
      run.output,
    );
  });

  // A prerelease must never become what a plain `npm install` resolves to.
  for (const [version, tag] of [["0.1.9", "latest"], ["0.1.9-rc1", "next"], ["0.2.0-beta.2", "next"]]) {
    test(`publishes ${version} under the ${tag} tag`, async (t) => {
      const run = await publish(t, version);

      assert.deepEqual(run.registry.live.map((p) => p.tag), Array(7).fill(tag), run.output);
    });
  }

  // ---------------------------------------------------------------- refusals

  // All seven or nothing. The three packages before the refused one are
  // accepted, and still must not go out: a release is one consistent set.
  test("uploads nothing when the registry refuses the run for one package", async (t) => {
    const trusted = everyPackage.filter((name) => name !== "@intuizi/cli-linux-x64");
    const run = await publish(t, "0.1.9", { trusted });

    assert.notEqual(run.status, 0, run.output);
    assert.deepEqual(run.live, []);
  });

  // One run has to say everything that is wrong, with the registry's own
  // reason, so the entries can be fixed in one go. This is the state the
  // registry was in when the script was written: only the launcher trusted.
  test("names every refused package with the registry's reason", async (t) => {
    const run = await publish(t, "0.1.9", { trusted: ["@intuizi/cli"] });

    assert.deepEqual(Object.keys(run.refused), [
      "@intuizi/cli-darwin-arm64",
      "@intuizi/cli-darwin-x64",
      "@intuizi/cli-linux-arm64",
      "@intuizi/cli-linux-x64",
      "@intuizi/cli-win32-arm64",
      "@intuizi/cli-win32-x64",
    ], run.output);
    for (const reason of Object.values(run.refused)) {
      assert.match(reason, /OIDC token exchange error - package not found/);
    }
  });

  // No id-token permission, or not a workflow run at all: npm attempts no
  // exchange and says nothing about one. A login on the machine must not turn
  // that into a publish, which is what keeps a release off a laptop.
  test("uploads nothing when npm attempts no OIDC exchange, even with a login to fall back on", async (t) => {
    const run = await publish(t, "0.1.9", { login: true }, { idToken: false });

    assert.notEqual(run.status, 0, run.output);
    assert.deepEqual(run.live, []);
    assert.equal(Object.keys(run.refused).length, 7, run.output);
  });

  // A refused exchange falls back to whatever login is configured. Uploading
  // then would work, and would quietly put the release out under a person's
  // account.
  test("uploads nothing through a login when the registry refuses the run", async (t) => {
    const run = await publish(t, "0.1.9", { trusted: [], login: true });

    assert.notEqual(run.status, 0, run.output);
    assert.deepEqual(run.live, []);
  });

  // --------------------------------------------------------------- re-runs

  // Re-running the job after part of a release went out must finish the set,
  // not fail on the versions that are already there.
  test("leaves a version that is already on the registry alone", async (t) => {
    const live = [{ name: "@intuizi/cli-darwin-arm64", version: "0.1.9", tag: "latest" }];
    const run = await publish(t, "0.1.9", { live });

    assert.equal(run.status, 0, run.output);
    assert.deepEqual(run.live, [
      "@intuizi/cli-darwin-arm64@0.1.9",
      "@intuizi/cli-darwin-x64@0.1.9",
      "@intuizi/cli-linux-arm64@0.1.9",
      "@intuizi/cli-linux-x64@0.1.9",
      "@intuizi/cli-win32-arm64@0.1.9",
      "@intuizi/cli-win32-x64@0.1.9",
      "@intuizi/cli@0.1.9",
    ]);
  });

  // The same for a prerelease, where npm itself would not notice the version
  // is there until the upload failed.
  test("leaves a prerelease that is already on the registry alone", async (t) => {
    const live = [{ name: "@intuizi/cli-darwin-arm64", version: "0.1.9-rc1", tag: "next" }];
    const run = await publish(t, "0.1.9-rc1", { live });

    assert.equal(run.status, 0, run.output);
    assert.equal(run.live.length, 7, run.output);
  });

  // What is already out needs no permission, so losing it afterwards must not
  // hold up the rest.
  test("does not ask about a version that is already on the registry", async (t) => {
    const live = [{ name: "@intuizi/cli-darwin-arm64", version: "0.1.9", tag: "latest" }];
    const trusted = everyPackage.filter((name) => name !== "@intuizi/cli-darwin-arm64");
    const run = await publish(t, "0.1.9", { live, trusted });

    assert.equal(run.status, 0, run.output);
    assert.equal(run.live.length, 7, run.output);
  });

  // An earlier version being live says nothing about this one.
  test("does not mistake an earlier live version for this one", async (t) => {
    const live = everyPackage.map((name) => ({ name, version: "0.1.8", tag: "latest" }));
    const run = await publish(t, "0.1.9", { live });

    assert.equal(run.status, 0, run.output);
    assert.equal(run.live.filter((id) => id.endsWith("@0.1.9")).length, 7, run.output);
  });

  test("changes nothing when the whole release is already on the registry", async (t) => {
    const live = everyPackage.map((name) => ({ name, version: "0.1.9", tag: "latest" }));
    const run = await publish(t, "0.1.9", { live, trusted: [] });

    assert.equal(run.status, 0, run.output);
    assert.deepEqual(run.registry.live, live);
  });

  // -------------------------------------------------------------- failures

  // A release that is not all there must be a red run, and the launcher must
  // not go out ahead of a platform package it depends on.
  test("fails, and holds the launcher back, when a platform package cannot be published", async (t) => {
    const run = await publish(t, "0.1.9", { broken: ["@intuizi/cli-linux-x64"] });

    assert.notEqual(run.status, 0, run.output);
    assert.deepEqual(run.live, [
      "@intuizi/cli-darwin-arm64@0.1.9",
      "@intuizi/cli-darwin-x64@0.1.9",
      "@intuizi/cli-linux-arm64@0.1.9",
    ]);
  });

  test("refuses to run without the packages directory", async (t) => {
    const run = await publish(t, "0.1.9", {}, { args: [] });

    assert.notEqual(run.status, 0);
    assert.match(run.output, /usage: .*publish\.sh/);
    assert.deepEqual(run.live, []);
  });

  // The 0.0.x tags exercise the pipeline, and a version number spent on one
  // can never be reused, so nothing below 0.1.0 may reach the registry.
  test("uploads nothing below 0.1.0", async (t) => {
    const run = await publish(t, "0.0.3");

    assert.equal(run.status, 0, run.output);
    assert.deepEqual(run.live, []);
    assert.match(run.output, /^::warning::.*0\.0\.3/m);
  });
});
