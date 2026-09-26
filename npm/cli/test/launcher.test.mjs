// Tests for bin/intuizi.js, the launcher @intuizi/cli installs as `intuizi`.
// Run with: node --test npm/cli/test/
//
// Each test lays the launcher out beside a fake platform package whose
// "binary" is a shell script, so the launcher's own contract is what is under
// test: exit codes and signals pass through as if the binary ran bare.

import { test } from "node:test";
import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { copyFileSync, mkdirSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));
const launcher = join(here, "..", "bin", "intuizi.js");
const posix = process.platform !== "win32";

// The fake binary: `exit N` exits N; `wait` prints "ready", then runs until
// SIGTERM, which it answers the way the Go binary does, with exit 143.
const fakeBinary = `#!/bin/sh
case "$1" in
  exit) exit "$2" ;;
  wait)
    trap 'exit 143' TERM
    echo ready
    while :; do sleep 0.05; done ;;
esac
`;

function layout(t) {
  const root = mkdtempSync(join(tmpdir(), "intuizi-launcher-"));
  t.after(() => rmSync(root, { recursive: true, force: true }));

  mkdirSync(join(root, "bin"));
  copyFileSync(launcher, join(root, "bin", "intuizi.js"));

  const pkg = join(root, "node_modules", "@intuizi", `cli-${process.platform}-${process.arch}`);
  mkdirSync(join(pkg, "bin"), { recursive: true });
  writeFileSync(join(pkg, "package.json"), JSON.stringify({ name: `@intuizi/cli-${process.platform}-${process.arch}` }));
  writeFileSync(join(pkg, "bin", "intuizi"), fakeBinary, { mode: 0o755 });

  return join(root, "bin", "intuizi.js");
}

// start runs the launcher as the leader of its own process group, so a test
// can signal it alone (pid) or the whole group (-pid), and can always clean
// the group up, fake binary included.
function start(t, shim, args) {
  const proc = spawn(process.execPath, [shim, ...args], {
    detached: true,
    stdio: ["ignore", "pipe", "inherit"],
  });
  t.after(() => {
    try {
      process.kill(-proc.pid, "SIGKILL");
    } catch {
      // Already gone, which is the passing case.
    }
  });
  const exited = new Promise((resolve) => proc.on("exit", (code, signal) => resolve({ code, signal })));
  const ready = new Promise((resolve) => {
    let seen = "";
    proc.stdout.on("data", (chunk) => {
      seen += chunk;
      if (seen.includes("ready")) resolve();
    });
  });
  return { proc, exited, ready };
}

function within(ms, promise, what) {
  let timer;
  const timeout = new Promise((_, reject) => {
    timer = setTimeout(() => reject(new Error(`${what}: nothing after ${ms}ms`)), ms);
  });
  return Promise.race([promise, timeout]).finally(() => clearTimeout(timer));
}

test("passes the binary's exit code through", { skip: !posix && "the fake binary is a shell script" }, async (t) => {
  const shim = layout(t);
  const { exited } = start(t, shim, ["exit", "2"]);
  assert.deepEqual(await within(5000, exited, "exit"), { code: 2, signal: null });
});

// A supervisor - Docker, systemd, a CI runner - sends SIGTERM to the process
// it started, which is the launcher. It must reach the binary.
test("passes a SIGTERM sent to the launcher alone on to the binary", { skip: !posix && "POSIX signals" }, async (t) => {
  const shim = layout(t);
  const { proc, exited, ready } = start(t, shim, ["wait"]);
  await within(5000, ready, "ready");

  process.kill(proc.pid, "SIGTERM");
  assert.deepEqual(await within(5000, exited, "exit after SIGTERM"), { code: 143, signal: null });
});

// timeout(1) and kill -TERM -- -<pgid> signal the launcher and the binary
// together; that must still end in the binary's 143.
test("exits 143 when the whole process group gets SIGTERM", { skip: !posix && "POSIX signals" }, async (t) => {
  const shim = layout(t);
  const { proc, exited, ready } = start(t, shim, ["wait"]);
  await within(5000, ready, "ready");

  process.kill(-proc.pid, "SIGTERM");
  assert.deepEqual(await within(5000, exited, "exit after a group SIGTERM"), { code: 143, signal: null });
});
