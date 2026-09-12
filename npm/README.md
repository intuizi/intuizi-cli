# npm packaging

`build.mjs` turns goreleaser's `dist/` into the seven npm packages described
at the top of that file: `@intuizi/cli` plus one binary-only package per
platform. The release workflow runs it after goreleaser and publishes the
result; `cli/` holds the launcher and README that go into `@intuizi/cli`.

To try it locally against a snapshot build:

```bash
goreleaser release --snapshot --clean
node npm/build.mjs --dist dist --out dist/npm
cd dist/npm/@intuizi && for p in cli-*; do (cd "$p" && npm pack --silent); done
cd cli && npm pack --silent
```

Then install the launcher and this machine's platform package from the
tarballs in one command, which is what CI does on every pull request:

```bash
npm install -g --prefix /tmp/intuizi-npm \
  dist/npm/@intuizi/cli-linux-x64/intuizi-cli-linux-x64-*.tgz \
  dist/npm/@intuizi/cli/intuizi-cli-*.tgz
/tmp/intuizi-npm/bin/intuizi version
```
