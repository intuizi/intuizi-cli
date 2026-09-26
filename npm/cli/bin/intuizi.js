#!/usr/bin/env node
"use strict";

// Shim installed as `intuizi` by @intuizi/cli. It finds the platform package
// npm chose for this machine and runs the real binary inside it, passing
// through arguments, stdio, exit code and signals unchanged, so scripts see
// the same contract they would from the bare binary.

const { spawn } = require("node:child_process");
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

// Asynchronous, so the handlers below can run while the binary does.
const child = spawn(bin, process.argv.slice(2), { stdio: "inherit", windowsHide: true });

// Ctrl-C reaches the child through the shared terminal. Ignore it here so the
// binary, which maps it to exit 130, decides the exit code rather than Node
// dying first with its own.
process.on("SIGINT", () => {});

// A supervisor - Docker, systemd, a CI runner, kill <pid> - sends SIGTERM or
// SIGHUP to the process it started, which is this one, so pass it on and let
// the binary decide the exit code. When the whole process group was signalled
// the binary has it already, and a second SIGTERM still ends in 143.
const forwarded = process.platform === "win32" ? ["SIGTERM"] : ["SIGTERM", "SIGHUP"];
for (const sig of forwarded) {
  process.on(sig, () => {
    if (child.exitCode === null && child.signalCode === null) {
      child.kill(sig);
    }
  });
}

child.on("error", (err) => {
  process.stderr.write(`intuizi: could not run ${bin}: ${err.message}\n`);
  process.exit(1);
});

child.on("exit", (code, signal) => {
  if (signal) {
    // The binary died from a signal it did not catch. Report 128+N the way a
    // shell would, so callers cannot tell the shim was there.
    process.exit(128 + (os.constants.signals[signal] || 0));
  }
  process.exit(code === null ? 1 : code);
});
