# Aruing

A Kubernetes operations agent that answers with tools and traceable evidence.

```sh
npm install -g @aruing/aruing
aruing version
aruing connect
aruing chat
```

Requires Node.js 22.14 or later and kubectl configured for your cluster.
Supported npm platforms: macOS arm64, macOS x64, and Linux x64.
Other platforms can use the [GitHub Release downloads](https://github.com/Aruing/Aruing/releases).

Each platform package downloads the matching version's official GitHub Release
archive and verifies it against that release's SHA-256 checksums before installation.
Installation needs HTTPS access to GitHub and its release asset hosts, and `tar` on PATH.
Optional dependencies and install scripts must be enabled.

If installation fails, view the installer error and retry:

```sh
npm install -g @aruing/aruing --include=optional --ignore-scripts=false --foreground-scripts
```

Upgrade npm-managed installations with `npm update -g @aruing/aruing`.
`aruing update` refuses to replace an npm-managed binary.

See the [project documentation](https://github.com/Aruing/Aruing#readme) for configuration and usage.
