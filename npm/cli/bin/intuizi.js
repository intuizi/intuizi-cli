#!/usr/bin/env node
"use strict";

// Shim installed as `intuizi` by @intuizi/cli. It finds the platform package
// npm chose for this machine and runs the real binary inside it, passing
// through arguments, stdio, exit code and signals unchanged, so scripts see
// the same contract they would from the bare binary.

const { spawnSync } = require("node:child_process");
const os = require("node:os");
const path = require("node:path");

const key = `${process.platform}-${process.arch}`;
const pkg = `@intuizi/cli-${key}`;
const exe = process.platform === "win32" ? "intuizi.exe" : "intuizi";

let bin;
try {
  bin = path.join(path.dirname(require.resolve(`${pkg}/package.json`)), "bin", exe);
} catch {
  process.stderr.write(
    `intuizi: no binary installed for ${key}.\n` +
      `\n` +
      `The binary ships in ${pkg}, an optional dependency of @intuizi/cli.\n` +
      `npm skips it when run with --no-optional or --omit=optional, and it does\n` +
      `not exist for platforms other than macOS, Linux and Windows on x64 and\n` +
      `arm64. Reinstall with optional dependencies enabled, or download a binary\n` +
      `from https://github.com/intuizi/intuizi-cli/releases\n`,
  );
  process.exit(1);
}

// Ctrl-C reaches the child through the shared terminal. Ignore it here so the
// binary, which maps it to exit 130, decides the exit code rather than Node
// dying first with its own.
process.on("SIGINT", () => {});
process.on("SIGTERM", () => {});

const result = spawnSync(bin, process.argv.slice(2), { stdio: "inherit", windowsHide: true });

if (result.error) {
  process.stderr.write(`intuizi: could not run ${bin}: ${result.error.message}\n`);
  process.exit(1);
}
if (result.signal) {
  // The binary died from a signal it did not catch. Report 128+N the way a
  // shell would, so callers cannot tell the shim was there.
  process.exit(128 + (os.constants.signals[result.signal] || 0));
}
process.exit(result.status === null ? 1 : result.status);
