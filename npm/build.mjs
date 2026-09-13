#!/usr/bin/env node
// Builds the npm packages for one release out of goreleaser's dist/ directory.
//
// npm cannot ship one package with six binaries in it without every install
// paying for all six, so the layout is the one esbuild established:
//
//   @intuizi/cli                the package people install. It holds a Node
//                               shim, bin/intuizi.js, that execs the binary,
//                               and lists every platform package below as an
//                               optionalDependency pinned to the same version.
//   @intuizi/cli-darwin-arm64   one per platform, holding only the binary and
//   @intuizi/cli-darwin-x64     declaring os/cpu, so npm installs exactly the
//   @intuizi/cli-linux-arm64    one that matches the machine and skips the rest.
//   @intuizi/cli-linux-x64
//   @intuizi/cli-win32-arm64
//   @intuizi/cli-win32-x64
//
// No postinstall step and no download at install time: the binary arrives in
// the tarball, which works behind proxies and in offline mirrors and leaves
// nothing for a compromised network to substitute.
//
// Usage:
//   node npm/build.mjs --dist dist --out dist/npm --version 0.1.0
//
// --version defaults to the version goreleaser recorded in dist/metadata.json,
// which is the tag without its leading v. Every package gets the same version,
// and the platform pins in @intuizi/cli use it exactly, so a release is one
// consistent set or nothing.

import {
  chmodSync, copyFileSync, existsSync, mkdirSync, readFileSync, rmSync, writeFileSync,
} from "node:fs";
import { dirname, isAbsolute, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));
const repo = "https://github.com/intuizi/intuizi-cli";

const args = parseArgs(process.argv.slice(2));
const dist = resolve(args.dist ?? "dist");
const out = resolve(args.out ?? join(dist, "npm"));
const version = args.version ?? versionFromMetadata(dist);

if (!/^\d+\.\d+\.\d+(-[0-9A-Za-z.-]+)?$/.test(version)) {
  fail(`"${version}" is not a semver version npm will accept`);
}

// goreleaser's GOOS/GOARCH to Node's process.platform/process.arch.
const platforms = {
  darwin: "darwin",
  linux: "linux",
  windows: "win32",
};
const arches = {
  amd64: "x64",
  arm64: "arm64",
};

const artifacts = JSON.parse(readFileSync(join(dist, "artifacts.json"), "utf8"));
const binaries = artifacts.filter((a) => a.type === "Binary" && a.extra?.ID === "intuizi");
if (binaries.length === 0) fail(`no binaries with build id "intuizi" in ${dist}/artifacts.json`);

const shared = {
  version,
  homepage: `${repo}#readme`,
  repository: { type: "git", url: `git+${repo}.git` },
  bugs: `${repo}/issues`,
  license: "SEE LICENSE IN LICENSE",
  publishConfig: { access: "public" },
};

rmSync(out, { recursive: true, force: true });

const platformPackages = {};
for (const b of binaries) {
  const os = platforms[b.goos];
  const cpu = arches[b.goarch];
  if (!os || !cpu) {
    console.error(`skipping ${b.goos}/${b.goarch}: no npm platform mapping`);
    continue;
  }
  const name = `@intuizi/cli-${os}-${cpu}`;
  const dir = join(out, name);
  mkdirSync(join(dir, "bin"), { recursive: true });

  const src = locate(b.path);
  const exe = os === "win32" ? "intuizi.exe" : "intuizi";
  copyFileSync(src, join(dir, "bin", exe));
  chmodSync(join(dir, "bin", exe), 0o755);

  copyLicenses(dir);
  writePackage(dir, {
    name,
    description: `Intuizi CLI binary for ${os} ${cpu}. Install @intuizi/cli, not this package.`,
    ...shared,
    os: [os],
    cpu: [cpu],
    files: ["bin/", "LICENSE", "THIRD_PARTY_NOTICES"],
  });
  platformPackages[name] = version;
  console.log(`${name}  <-  ${b.path}`);
}

// The package people install.
const main = join(out, "@intuizi/cli");
mkdirSync(join(main, "bin"), { recursive: true });
copyFileSync(join(here, "cli/bin/intuizi.js"), join(main, "bin/intuizi.js"));
chmodSync(join(main, "bin/intuizi.js"), 0o755);
copyFileSync(join(here, "cli/README.md"), join(main, "README.md"));
copyLicenses(main);
writePackage(main, {
  name: "@intuizi/cli",
  description: "Official Intuizi CLI: a single-binary client for the Intuizi API v2",
  keywords: ["intuizi", "cli", "audiences", "activations", "geo-signals"],
  ...shared,
  bin: { intuizi: "bin/intuizi.js" },
  files: ["bin/", "LICENSE", "THIRD_PARTY_NOTICES"],
  engines: { node: ">=18" },
  optionalDependencies: sortKeys(platformPackages),
});
console.log(`@intuizi/cli  ${version}  with ${Object.keys(platformPackages).length} platform packages`);
console.log(`packages written under ${out}`);

// ----------------------------------------------------------------- helpers

// The license is proprietary and the binary links open-source libraries, so
// both notices travel inside every package, not only in the repository.
function copyLicenses(dir) {
  for (const f of ["LICENSE", "THIRD_PARTY_NOTICES"]) {
    copyFileSync(join(here, "..", f), join(dir, f));
  }
}

function writePackage(dir, pkg) {
  writeFileSync(join(dir, "package.json"), JSON.stringify(pkg, null, 2) + "\n");
}

// goreleaser records artifact paths relative to the directory it ran in, which
// is the repository root when dist is "dist". Try that first, then dist itself,
// then the working directory, so the script runs from either place.
function locate(p) {
  const candidates = isAbsolute(p) ? [p] : [resolve(dirname(dist), p), resolve(dist, p), resolve(p)];
  const hit = candidates.find((c) => existsSync(c));
  if (!hit) fail(`artifact ${p} not found; tried ${candidates.join(", ")}`);
  return hit;
}

function versionFromMetadata(distDir) {
  const file = join(distDir, "metadata.json");
  if (!existsSync(file)) fail(`no --version given and ${file} does not exist`);
  const meta = JSON.parse(readFileSync(file, "utf8"));
  if (!meta.version) fail(`${file} carries no version`);
  return meta.version;
}

function sortKeys(obj) {
  return Object.fromEntries(Object.entries(obj).sort(([a], [b]) => a.localeCompare(b)));
}

function parseArgs(argv) {
  const parsed = {};
  for (let i = 0; i < argv.length; i++) {
    const m = /^--([a-z]+)(?:=(.*))?$/.exec(argv[i]);
    if (!m) fail(`unexpected argument ${argv[i]}`);
    parsed[m[1]] = m[2] ?? argv[++i];
    if (parsed[m[1]] === undefined) fail(`--${m[1]} needs a value`);
  }
  return parsed;
}

function fail(msg) {
  console.error(`npm/build.mjs: ${msg}`);
  process.exit(1);
}
