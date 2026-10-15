# Install hloop on Windows (PowerShell):
#   $env:HLOOP_REPO="eternitycircle/hloop"; irm https://raw.githubusercontent.com/eternitycircle/hloop/main/scripts/install.ps1 | iex
$ErrorActionPreference = "Stop"
$repo = if ($env:HLOOP_REPO) { $env:HLOOP_REPO } else { "eternitycircle/hloop" }
if ($repo -like "OWNER/*") { throw "Set `$env:HLOOP_REPO='<github-user>/<repo>' (this script has not been customised yet)." }
$arch = if ($env:PROCESSOR_ARCHITECTURE -eq "ARM64") { "arm64" } else { "amd64" }
$version = if ($env:HLOOP_VERSION) { $env:HLOOP_VERSION } else {
  (Invoke-WebRequest -UseBasicParsing -MaximumRedirection 0 -ErrorAction SilentlyContinue "https://github.com/$repo/releases/latest").Headers.Location -replace '.*/tag/', ''
}
$ver = $version.TrimStart("v")
$file = "hloop_${ver}_windows_${arch}.zip"
$base = "https://github.com/$repo/releases/download/$version"
$tmp = Join-Path $env:TEMP ("hloop-" + [guid]::NewGuid())
New-Item -ItemType Directory $tmp | Out-Null
try {
  Write-Host "Downloading $file ..."
  Invoke-WebRequest -UseBasicParsing "$base/$file" -OutFile "$tmp\$file"
  Invoke-WebRequest -UseBasicParsing "$base/checksums.txt" -OutFile "$tmp\checksums.txt"
  $want = ((Get-Content "$tmp\checksums.txt") | Where-Object { $_ -match [regex]::Escape($file) + '$' }) -split '\s+' | Select-Object -First 1
  $got = (Get-FileHash "$tmp\$file" -Algorithm SHA256).Hash.ToLower()
  if (-not $want -or $want.ToLower() -ne $got) { throw "Checksum mismatch: refusing to install." }
  Expand-Archive "$tmp\$file" -DestinationPath $tmp
  $dest = Join-Path $env:LOCALAPPDATA "Programs\hloop"
  New-Item -ItemType Directory -Force $dest | Out-Null
  Copy-Item "$tmp\hloop_${ver}_windows_${arch}\hloop.exe" "$dest\hloop.exe" -Force
  $userPath = [Environment]::GetEnvironmentVariable("Path", "User")
  if (($userPath -split ";") -notcontains $dest) {
    [Environment]::SetEnvironmentVariable("Path", "$userPath;$dest", "User")
    Write-Host "Added $dest to your PATH. Open a new terminal to use it."
  }
  Write-Host "Installed $dest\hloop.exe ($version)"
  Write-Host "Next: cd into a project and run  hloop init  (new)  or  hloop adopt  (already in progress)"
} finally { Remove-Item -Recurse -Force $tmp -ErrorAction SilentlyContinue }
