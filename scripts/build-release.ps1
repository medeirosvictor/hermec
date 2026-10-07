<#
.SYNOPSIS
  Builds version-stamped Hermec release artifacts into dist/.

.DESCRIPTION
  Usage (from anywhere; the script cd's to the repo root):
    pwsh scripts/build-release.ps1 -Tag v0.1.0 [-SkipLinux] [-Msys2Bin C:\msys64\ucrt64\bin]

  Artifacts:
    hermec-server-linux-amd64, hermec-server-linux-arm64   (CGO off; skipped by -SkipLinux)
    hermec-server-windows-amd64.exe                        (CGO off)
    hermec-windows-amd64.zip  = hermec.exe + runtime DLLs + README.txt (CGO on)
    SHA256SUMS.txt over all of the above

  Every binary is stamped via -ldflags "-s -w -X .../version.Version=<Tag>".

  The client DLL set is NOT hardcoded. It is discovered by walking the import
  graph of hermec.exe and keeping only DLLs that live in the msys2 ucrt64 bin
  dir (Windows system DLLs are excluded). Preferred tool: ntldd -R
  (pacman -S mingw-w64-ucrt-x86_64-ntldd). Fallback when ntldd is absent: a
  recursive `objdump -p <file>` walk of "DLL Name:" entries, resolving each
  name against the msys2 bin dir. For humans, the EXPECTED set is roughly
  libopus, libopusfile, libogg, libwinpthread and the gcc runtime
  (libgcc_s_seh) -- but whatever the walk finds is what ships.

  -SkipLinux: omit the linux cross-builds (e.g. for a Windows-only CI job).
  Non-interactive; any failure terminates with a non-zero exit code.
#>
param(
    [Parameter(Mandatory = $true)][string]$Tag,
    [switch]$SkipLinux,
    [string]$Msys2Bin = 'C:\msys64\ucrt64\bin'
)
$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

$repo = Split-Path -Parent $PSScriptRoot
Set-Location $repo

function Invoke-Native {
    param([string]$Exe, [string[]]$Arguments)
    & $Exe @Arguments
    if ($LASTEXITCODE -ne 0) { throw "$Exe $($Arguments -join ' ') failed with exit code $LASTEXITCODE" }
}

$ldflags = "-s -w -X github.com/medeirosvictor/hermec/version.Version=$Tag"
$dist = Join-Path $repo 'dist'
if (Test-Path $dist) { Remove-Item -Recurse -Force $dist }
New-Item -ItemType Directory -Path $dist | Out-Null

function Build-Go {
    param([string]$Pkg, [string]$Out, [string]$GoOS, [string]$GoArch, [string]$Cgo)
    $saved = @{ GOOS = $env:GOOS; GOARCH = $env:GOARCH; CGO_ENABLED = $env:CGO_ENABLED }
    try {
        $env:GOOS = $GoOS; $env:GOARCH = $GoArch; $env:CGO_ENABLED = $Cgo
        Write-Host "==> $Out"
        Invoke-Native go @('build', '-trimpath', '-ldflags', $ldflags, '-o', (Join-Path $dist $Out), $Pkg)
    } finally {
        foreach ($k in $saved.Keys) {
            if ($null -eq $saved[$k]) { Remove-Item "Env:$k" -ErrorAction SilentlyContinue } else { Set-Item "Env:$k" $saved[$k] }
        }
    }
}

# --- servers (pure Go) ---
if (-not $SkipLinux) {
    Build-Go './cmd/hermec-server' 'hermec-server-linux-amd64' 'linux' 'amd64' '0'
    Build-Go './cmd/hermec-server' 'hermec-server-linux-arm64' 'linux' 'arm64' '0'
}
Build-Go './cmd/hermec-server' 'hermec-server-windows-amd64.exe' 'windows' 'amd64' '0'

# --- client (CGO: needs msys2 ucrt64 gcc + opus/opusfile via pkg-config) ---
if (-not (Test-Path (Join-Path $Msys2Bin 'gcc.exe'))) { throw "gcc not found in $Msys2Bin (install msys2 ucrt64 toolchain or pass -Msys2Bin)" }
$savedPath = $env:PATH
try {
    $env:PATH = "$Msys2Bin;$env:PATH"
    Build-Go './cmd/hermec' 'hermec.exe' 'windows' 'amd64' '1'
} finally { $env:PATH = $savedPath }

# --- DLL discovery ---
$msysRoot = (Resolve-Path $Msys2Bin).Path.TrimEnd('\')
$clientExe = Join-Path $dist 'hermec.exe'
$dlls = [System.Collections.Generic.SortedSet[string]]::new([StringComparer]::OrdinalIgnoreCase)
$ntldd = Join-Path $Msys2Bin 'ntldd.exe'
if (Test-Path $ntldd) {
    Write-Host '==> DLL walk via ntldd -R'
    $out = & $ntldd -R $clientExe
    if ($LASTEXITCODE -ne 0) { throw 'ntldd failed' }
    foreach ($line in $out) {
        if ($line -match '=>\s+(.+?)\s+\(0x[0-9a-fA-F]+\)') {
            $p = $Matches[1].Trim()
            if ($p.StartsWith($msysRoot, [StringComparison]::OrdinalIgnoreCase)) { [void]$dlls.Add($p) }
        }
    }
} else {
    Write-Host '==> DLL walk via objdump (ntldd not found)'
    $objdump = Join-Path $Msys2Bin 'objdump.exe'
    if (-not (Test-Path $objdump)) { throw 'neither ntldd nor objdump found in msys2 bin' }
    $queue = [System.Collections.Generic.Queue[string]]::new()
    $queue.Enqueue($clientExe)
    $seen = @{}
    while ($queue.Count -gt 0) {
        $f = $queue.Dequeue()
        $out = & $objdump -p $f
        if ($LASTEXITCODE -ne 0) { throw "objdump failed on $f" }
        foreach ($line in $out) {
            if ($line -match 'DLL Name:\s+(\S+)') {
                $name = $Matches[1]
                $cand = Join-Path $Msys2Bin $name
                if ((Test-Path $cand) -and -not $seen.ContainsKey($name.ToLower())) {
                    $seen[$name.ToLower()] = $true
                    [void]$dlls.Add($cand)
                    $queue.Enqueue($cand)
                }
            }
        }
    }
}
if ($dlls.Count -eq 0) { throw 'DLL walk found no msys2 DLLs; refusing to ship a client that cannot run' }
Write-Host ("    bundling: " + (($dlls | ForEach-Object { Split-Path $_ -Leaf }) -join ', '))

# --- client zip ---
$stage = Join-Path $dist 'stage'
New-Item -ItemType Directory -Path $stage | Out-Null
Copy-Item $clientExe $stage
foreach ($d in $dlls) { Copy-Item $d $stage }
@"
Hermec $Tag (Windows, 64-bit)

How to run
  1. Extract this whole zip into a folder (keep hermec.exe and the .dll files together).
  2. Double-click hermec.exe.

Windows SmartScreen
  The binary is not code-signed, so Windows may show "Windows protected your PC".
  Click "More info", then "Run anyway".

Audio
  Use headphones. Without them your microphone will pick up the speakers and
  other people will hear echo.
"@ | Set-Content -Encoding ASCII (Join-Path $stage 'README.txt')
Compress-Archive -Path (Join-Path $stage '*') -DestinationPath (Join-Path $dist 'hermec-windows-amd64.zip')
Remove-Item -Recurse -Force $stage
Remove-Item $clientExe   # only the zip ships the client

# --- checksums ---
$lines = Get-ChildItem $dist -File | Where-Object Name -ne 'SHA256SUMS.txt' | Sort-Object Name | ForEach-Object {
    $h = (Get-FileHash -Algorithm SHA256 $_.FullName).Hash.ToLower()
    "$h  $($_.Name)"
}
[System.IO.File]::WriteAllText((Join-Path $dist 'SHA256SUMS.txt'), (($lines -join "`n") + "`n"))
Write-Host "Done: $Tag"
