# aruing 一行安装脚本（Windows PowerShell）
# 用法：irm https://raw.githubusercontent.com/Aruing/Aruing/main/scripts/install.ps1 | iex
# 流程与 install.sh 同构：平台检测 → 列表 API 解析最新版本 → 下载 → SHA256 校验 →
#       解压到 %USERPROFILE%\.aruing\bin → PATH 检测与提示
# 决策 4：仅 amd64；arm64 Windows 明确报错。TUI 在 Windows 为 experimental（决策 6）

$ErrorActionPreference = 'Stop'

$Repo = 'Aruing/Aruing'
$InstallDir = Join-Path $env:USERPROFILE '.aruing\bin'

# 平台检测： PROCESSOR_ARCHITECTURE 报告本机架构
$arch = $env:PROCESSOR_ARCHITECTURE
if ($arch -ne 'AMD64') {
    Write-Error "aruing install: unsupported architecture '$arch' (only windows amd64 is published)"
}

# 版本解析： 列表 API 第一个非 draft 项即最新（含 pre-release，与 install.sh 同策略）
$releases = Invoke-RestMethod -Uri "https://api.github.com/repos/$Repo/releases?per_page=10"
$release = $releases | Where-Object { -not $_.draft } | Select-Object -First 1
if (-not $release) {
    Write-Error 'aruing install: no published release found'
}
$tag = $release.tag_name

$assetName = 'aruing_windows_amd64.zip'
$asset = $release.assets | Where-Object { $_.name -eq $assetName }
if (-not $asset) {
    Write-Error "aruing install: asset $assetName not found in release $tag"
}

Write-Host "==> downloading $assetName ($tag)"
$tmp = New-Item -ItemType Directory -Force -Path (Join-Path $env:TEMP ([System.IO.Path]::GetRandomFileName()))
$zipPath = Join-Path $tmp $assetName
$sumPath = Join-Path $tmp 'checksums.txt'
Invoke-WebRequest -Uri $asset.browser_download_url -OutFile $zipPath
$sumAsset = $release.assets | Where-Object { $_.name -eq 'checksums.txt' }
Invoke-WebRequest -Uri $sumAsset.browser_download_url -OutFile $sumPath

# SHA256 校验： 从 checksums.txt 找到本产物的期望值（按规范名匹配，不得改名）
$expected = (Get-Content $sumPath) |
    Where-Object { $_ -match [regex]::Escape($assetName) } |
    Select-Object -First 1
if (-not $expected) {
    Write-Error "aruing install: no checksum entry for $assetName"
}
$expectedHash = ($expected -split '\s+')[0].Trim().ToLower()
$actualHash = (Get-FileHash -Path $zipPath -Algorithm SHA256).Hash.ToLower()
if ($expectedHash -ne $actualHash) {
    Write-Error "aruing install: checksum mismatch for $assetName (expected $expectedHash, got $actualHash)"
}
Write-Host "==> checksum OK"

# 解压与安装（免管理员： 用户级目录）
New-Item -ItemType Directory -Force -Path $InstallDir | Out-Null
Expand-Archive -Path $zipPath -DestinationPath $InstallDir -Force
Remove-Item -Recurse -Force $tmp

$exe = Join-Path $InstallDir 'aruing.exe'
Write-Host "==> installed: $exe"
& $exe version

# PATH 检测： 不在则打印可复制的用户级 PATH 写入命令（不自动写入）
$userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
if ($userPath -notlike "*$InstallDir*") {
    Write-Host ''
    Write-Host "==> $InstallDir is not in your PATH. Add it with:"
    Write-Host "       [Environment]::SetEnvironmentVariable('Path', [Environment]::GetEnvironmentVariable('Path','User') + ';$InstallDir', 'User')"
    Write-Host '       then restart your terminal.'
}
