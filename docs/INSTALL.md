# Installing hloop

## 1. From the zip you were given (works today, no GitHub needed)

The zip contains prebuilt binaries in `dist/`. Pick the archive for your machine.

**Linux / macOS**

```sh
unzip hloop-0.6.0.zip -d hloop-src && cd hloop-src
tar -xzf dist/hloop_0.6.0_linux_amd64.tar.gz        # macOS Apple Silicon: darwin_arm64, Intel: darwin_amd64, ARM Linux: linux_arm64
mkdir -p ~/.local/bin
install -m 0755 hloop_0.6.0_linux_amd64/hloop ~/.local/bin/hloop
export PATH="$HOME/.local/bin:$PATH"                 # add this line to ~/.profile (or ~/.zshrc) to keep it
hloop version
```

On macOS, a binary that came out of a browser-downloaded zip is quarantined as unsigned. If it's refused, run `xattr -d com.apple.quarantine ~/.local/bin/hloop`.

**Windows (PowerShell)**

```powershell
Expand-Archive hloop-0.6.0.zip hloop-src; cd hloop-src
Expand-Archive dist\hloop_0.6.0_windows_amd64.zip .      # windows_arm64 on ARM
New-Item -ItemType Directory -Force "$env:LOCALAPPDATA\Programs\hloop" | Out-Null
Copy-Item hloop_0.6.0_windows_amd64\hloop.exe "$env:LOCALAPPDATA\Programs\hloop\"
[Environment]::SetEnvironmentVariable("Path", "$([Environment]::GetEnvironmentVariable('Path','User'));$env:LOCALAPPDATA\Programs\hloop", "User")
# open a new terminal, then:
hloop version
```

SmartScreen may warn that the app is unrecognized; choose "More info" then "Run anyway".

## 2. Build from source

Needs Go 1.22 or newer.

```sh
go build -o hloop ./cmd/hloop        # Windows: go build -o hloop.exe ./cmd/hloop
```

Put the result anywhere on your PATH. `scripts/build-all.sh 0.6.0` builds all six platform archives into `dist/`.

## 3. The one-line install (after the repo is published)

Run `scripts/set-repo.sh <github-user>/<repo>` once, commit, and publish a release (see [PUBLISHING.md](PUBLISHING.md)). Then anyone can install with:

```sh
curl -fsSL https://raw.githubusercontent.com/OWNER/harness-loop/main/scripts/install.sh | sh
```

```powershell
irm https://raw.githubusercontent.com/OWNER/harness-loop/main/scripts/install.ps1 | iex
```

The scripts download the right archive from the latest GitHub Release, verify its checksum, and install to `~/.local/bin` (Linux/macOS) or `%LOCALAPPDATA%\Programs\hloop` (Windows, added to PATH). Pin a version with `HLOOP_VERSION=v0.6.0`. The repo must be public for these URLs to work without a login.

To install a specific version or a pre-release from a branch before merging:

```sh
curl -fsSL https://raw.githubusercontent.com/OWNER/harness-loop/<branch>/scripts/install.sh | HLOOP_VERSION=v0.6.0-rc1 sh
```

## Updating and uninstalling

Update: rerun the installer. Uninstall: delete the `hloop` binary; remove `.loop/` from projects if you want; run `hloop schedule remove` in each project and `hloop notify service remove` first if you installed those.
