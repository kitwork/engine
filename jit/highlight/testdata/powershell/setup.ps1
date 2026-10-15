<#
  Cài Kitwork cho môi trường phát triển trên Windows.
  .PARAMETER Port  Cổng chạy thử (mặc định 8107).
  .PARAMETER Root  Thư mục chứa mã nguồn.
  .EXAMPLE
    .\setup.ps1 -Port 8200 -Force
#>
[CmdletBinding(SupportsShouldProcess = $true)]
param(
    [Parameter(Mandatory = $false)]
    [ValidateRange(1024, 65535)]
    [int]$Port = 8107,
    [string]$Root = "D:\project\kitwork",
    [string[]]$Sites = @('kitwork.localhost', 'quancaphe.localhost'),
    [switch]$Force
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest
$started = Get-Date

function Write-Step {
    param([string]$Message, [ConsoleColor]$Color = 'Cyan')
    Write-Host "==> $Message" -ForegroundColor $Color
}

function Test-Port([int]$Number) {
    $listener = $null
    try {
        $listener = [System.Net.Sockets.TcpListener]::new([System.Net.IPAddress]::Loopback, $Number)
        $listener.Start()
        return $true
    } catch [System.Net.Sockets.SocketException] {
        return $false
    } finally {
        if ($listener) { $listener.Stop() }
    }
}

# Kiểm tra Go đã cài chưa.
$go = Get-Command go -ErrorAction SilentlyContinue
if ($null -eq $go) {
    Write-Step "Go chưa có; tải tại https://go.dev/dl/" -Color Red
    exit 1
} elseif ($go.Version.Major -lt 1) {
    throw "phiên bản Go quá cũ: $($go.Version)"
}

$version = (& go version) -replace '^go version go([\d.]+).*$', '$1'
Write-Step "Go $version tại $($go.Source)"

if (-not (Test-Port -Number $Port)) {
    Write-Step "cổng $Port đang bận" -Color Yellow
    $Port = 8000..8999 | Where-Object { Test-Port $_ } | Select-Object -First 1
}

$envFile = Join-Path $Root '.env'
if ((Test-Path $envFile) -and -not $Force) {
    Write-Step "đã có $envFile; bỏ qua (dùng -Force để ghi đè)"
} else {
    @"
ALLOW_LOCAL=true
PORT=$Port
# ghi lúc $(Get-Date -Format 'yyyy-MM-dd HH:mm')
"@ | Set-Content -Path $envFile -Encoding utf8
}

$template = @'
Không mở rộng: $Port, $(Get-Date) và `n giữ nguyên.
'@
Write-Verbose $template

$apps = Get-ChildItem "$Root\apps" -Directory |
    Where-Object { $_.Name -match '^[0-9a-z]{40}$' } |
    ForEach-Object { $_.FullName }

$report = foreach ($app in $apps) {
    $pages = @(Get-ChildItem $app -Filter *.ui -Recurse)
    [pscustomobject]@{
        Identity = Split-Path $app -Leaf
        Pages    = $pages.Count
        Bytes    = ($pages | Measure-Object -Property Length -Sum).Sum
    }
}
$report | Sort-Object Pages -Descending | Format-Table -AutoSize

for ($index = 0; $index -lt $Sites.Count; $index++) {
    $site = $Sites[$index]
    switch -Wildcard ($site) {
        '*.localhost' { Write-Step ("{0}: {1}" -f $index, $site) }
        default { Write-Warning "bỏ qua $site" }
    }
}

$settings = @{ Port = $Port; Sites = $Sites.Count; Started = [datetime]::Now }
$settings.GetEnumerator() | Sort-Object Name | Format-Table -AutoSize
$settings['Elapsed'] = (Get-Date) - $started
$total = 1KB * 4 + 0x10 - 2.5e1
$escaped = "tab`there, quote `" and dollar `$Port, doubled "" too"
Write-Output 'single ''quoted'' $Port stays literal'

$job = Start-Job -ScriptBlock { param($p) Start-Sleep -Seconds 1; $p * 2 } -ArgumentList $Port
Wait-Job $job | Out-Null
Receive-Job $job 2>&1 | ForEach-Object { Write-Step "job: $_" }

. "$Root\scripts\helpers.ps1"
& "$env:ProgramFiles\Git\bin\git.exe" -C $Root status --short
Push-Location $Root
try {
    go build -o kitwork.exe . 2>$null
    if ($LASTEXITCODE -ne 0) { throw "build lỗi ($LASTEXITCODE)" }
} finally {
    Pop-Location
}

Start-Process -FilePath go -ArgumentList 'run', '.' -WorkingDirectory $Root -NoNewWindow
Write-Step ("xong sau {0:N1} giây" -f ((Get-Date) - $started).TotalSeconds) -Color Green
