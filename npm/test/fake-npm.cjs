#!/usr/bin/env node
// A stand-in for the npm CLI, used by publish.test.mjs. It keeps a registry in
// the JSON file named by FAKE_NPM_REGISTRY and answers the two commands
// npm/publish.sh runs the way npm 11.21 and the registry do: same arguments,
// same output lines, same refusals.
//
// The registry file holds:
//   trusted  names with a trusted publisher entry that matches this run
//   live     versions on the registry, and who published each
//   broken   names every upload fails for, with a 500
//   login    true when the machine also holds an npm login, as a laptop does
"use strict";

const fs = require("node:fs");

const file = process.env.FAKE_NPM_REGISTRY;
const registry = JSON.parse(fs.readFileSync(file, "utf8"));
const argv = process.argv.slice(2);

const flag = (name) => argv.includes(name);
const option = (name) => (argv.includes(name) ? argv[argv.indexOf(name) + 1] : undefined);

function fail(code, message) {
  process.stderr.write(`npm error code ${code}\nnpm error ${message}\n`);
  process.exit(1);
}

// npm view <name>@<version> version: the version when it is live, a 404 and
// exit 1 when it is not.
if (argv[0] === "view" && argv[2] === "version") {
  const hit = registry.live.find((p) => `${p.name}@${p.version}` === argv[1]);
  if (!hit) fail("E404", `404 No match found for version ${argv[1].split("@").pop()}`);
  console.log(hit.version);
  process.exit(0);
}

// npm publish, run inside a package directory.
if (argv[0] === "publish") {
  const { name, version } = JSON.parse(fs.readFileSync("package.json", "utf8"));
  const prerelease = version.includes("-");
  const tag = option("--tag");

  if (prerelease && !tag) fail("EUSAGE", "You must specify a tag using --tag when publishing a prerelease version.");
  process.stderr.write(`npm notice name: ${name}\nnpm notice version: ${version}\n`);

  // npm attempts the OIDC exchange before every publish, a dry one included,
  // but only inside a workflow run whose job holds the id-token permission,
  // which is what puts these two variables in the environment. It says how the
  // exchange went only at the verbose level, and after a successful one it
  // also turns provenance on and says that too.
  const attempted = process.env.GITHUB_ACTIONS === "true"
    && process.env.ACTIONS_ID_TOKEN_REQUEST_URL && process.env.ACTIONS_ID_TOKEN_REQUEST_TOKEN;
  const accepted = Boolean(attempted) && registry.trusted.includes(name);
  if (attempted && option("--loglevel") === "verbose") {
    process.stderr.write(accepted
      ? "npm verbose oidc Successfully retrieved and set token\nnpm verbose oidc Enabling provenance\n"
      : "npm verbose oidc Failed token exchange request with body message: OIDC token exchange error - package not found\n");
  }

  const dry = flag("--dry-run");
  if (!accepted && !registry.login) {
    const needsLogin = "This command requires you to be logged in to https://registry.npmjs.org/";
    if (!dry) fail("ENEEDAUTH", needsLogin);
    process.stderr.write(`npm warn publish ${needsLogin} (dry-run)\n`);
  }

  // npm checks the versions it can see before it uploads, dry run or not, but
  // it leaves prereleases out of that list; for those only the upload fails.
  const live = registry.live.some((p) => p.name === name && p.version === version);
  if (live && (!prerelease || !dry)) {
    fail("EPUBLISHCONFLICT", `You cannot publish over the previously published versions: ${version}.`);
  }

  const access = option("--access") ?? "default";
  process.stderr.write(`npm notice Publishing to https://registry.npmjs.org/ with tag ${tag ?? "latest"} and ${access} access${dry ? " (dry-run)" : ""}\n`);
  if (dry) process.exit(0);

  if (registry.broken.includes(name)) fail("E500", "500 Internal Server Error");

  registry.live.push({
    name,
    version,
    tag: tag ?? "latest",
    access,
    provenance: flag("--provenance"),
    publisher: accepted ? "GitHub Actions" : "the login",
  });
  fs.writeFileSync(file, JSON.stringify(registry, null, 2));
  console.log(`+ ${name}@${version}`);
  process.exit(0);
}

process.stderr.write(`fake npm: no answer for: npm ${argv.join(" ")}\n`);
process.exit(64);
