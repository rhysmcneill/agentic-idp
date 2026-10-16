# Installing `idpctl`

`idpctl` is the CLI client for the control plane. It is released on its own `idpctl-vX.Y.Z` stream, separate from the platform images ([Decision 024](DECISIONS.md)), so its version does not match the control plane's. It holds no cloud credentials.

Every release publishes archives for:

| OS | Architectures |
|---|---|
| Linux | amd64, arm64, arm (v7) |
| macOS | amd64 (Intel), arm64 (Apple Silicon) |
| Windows | amd64, arm64 |

Files are named `idpctl_<version>_<os>_<arch>.tar.gz` (`.zip` on Windows), next to `idpctl_<version>_checksums.txt`, on the [Releases page](https://github.com/rhysmcneill/agentic-idp/releases) under the `idpctl-v*` tags.

## Install script (Linux and macOS)

```bash
curl -fsSL https://raw.githubusercontent.com/rhysmcneill/agentic-idp/main/scripts/install-idpctl.sh | sh
```

It detects your OS and architecture, downloads the latest `idpctl-v*` release, verifies its SHA-256 against the published checksums file, and installs to `/usr/local/bin` (or `~/.local/bin` if that isn't writable).

| Variable | Effect |
|---|---|
| `IDPCTL_VERSION` | Install a specific version, e.g. `IDPCTL_VERSION=0.3.0` |
| `IDPCTL_BIN_DIR` | Install somewhere else |

Reading a script before piping it to a shell is reasonable; [it is short](../scripts/install-idpctl.sh).

## Manual install

Linux or macOS:

```bash
VERSION=0.3.0 OS=linux ARCH=amd64
BASE=https://github.com/rhysmcneill/agentic-idp/releases/download/idpctl-v$VERSION
curl -fsSLO $BASE/idpctl_${VERSION}_${OS}_${ARCH}.tar.gz
curl -fsSLO $BASE/idpctl_${VERSION}_checksums.txt
grep " idpctl_${VERSION}_${OS}_${ARCH}.tar.gz\$" idpctl_${VERSION}_checksums.txt | shasum -a 256 -c -
tar -xzf idpctl_${VERSION}_${OS}_${ARCH}.tar.gz
sudo install -m 0755 idpctl_${VERSION}_${OS}_${ARCH}/idpctl /usr/local/bin/idpctl
```

Windows: download the `.zip` for your architecture, extract it, and put `idpctl.exe` on your `PATH`.

## Verify

```bash
idpctl --version
```

## First use

Point it at your control plane and create the admin (one-time):

```bash
idpctl setup --server https://idp.example.com
```

The rest of the sequence is in [ONBOARDING.md](ONBOARDING.md).

## Build from source

```bash
make build-cli CLI_VERSION=dev   # writes bin/idpctl
```
