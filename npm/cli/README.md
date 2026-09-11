# @intuizi/cli

The official command-line interface for the [Intuizi](https://intuizi.com)
data signal platform, packaged for npm.

```bash
npm install -g @intuizi/cli
intuizi auth login
intuizi audiences list
```

This package holds a small launcher. The CLI itself is a static Go binary
that npm fetches for your platform through one of the `@intuizi/cli-<os>-<cpu>`
optional dependencies (macOS, Linux and Windows, x64 and arm64). Nothing is
downloaded at install time beyond the packages themselves, so it works behind
proxies and in offline mirrors.

Documentation, examples and the source live at
<https://github.com/intuizi/intuizi-cli>. Binaries for other install methods
are on the [releases page](https://github.com/intuizi/intuizi-cli/releases).
