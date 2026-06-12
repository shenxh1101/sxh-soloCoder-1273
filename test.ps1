# filerename CLI Integration Test Suite
# Usage: powershell -File test.ps1 [binary_path]

param(
    [string]$BinPath = ".\filerename.exe"
)

$total = 0
$passed = 0
$failed = 0

function Test-Case {
    param(
        [string]$Name,
        [string[]]$CmdArgs,
        [int]$ExpectedExit = 0,
        [string[]]$MustContain = @(),
        [string[]]$MustNotContain = @()
    )

    $global:total++
    Write-Host -NoNewline "  [$global:total] $Name ... "

    try {
        $psi = New-Object System.Diagnostics.ProcessStartInfo
        $psi.FileName = (Resolve-Path $BinPath).Path
        $psi.Arguments = $CmdArgs -join " "
        $psi.RedirectStandardOutput = $true
        $psi.RedirectStandardError = $true
        $psi.UseShellExecute = $false
        $psi.CreateNoWindow = $true
        $psi.StandardOutputEncoding = [System.Text.Encoding]::UTF8
        $psi.StandardErrorEncoding = [System.Text.Encoding]::UTF8

        $p = [System.Diagnostics.Process]::Start($psi)
        $p.WaitForExit(30000)

        $stdout = $p.StandardOutput.ReadToEnd()
        $stderr = $p.StandardError.ReadToEnd()
        $all = $stdout + $stderr

        $exitOk = ($p.ExitCode -eq $ExpectedExit)
        $containsOk = $true
        foreach ($m in $MustContain) {
            if ($all -notmatch [regex]::Escape($m)) {
                $containsOk = $false
                break
            }
        }
        $notContainOk = $true
        foreach ($m in $MustNotContain) {
            if ($all -match [regex]::Escape($m)) {
                $notContainOk = $false
                break
            }
        }

        if ($exitOk -and $containsOk -and $notContainOk) {
            Write-Host "PASS" -ForegroundColor Green
            $global:passed++
        } else {
            Write-Host "FAIL" -ForegroundColor Red
            if (-not $exitOk) {
                Write-Host "    expected exit $ExpectedExit, got $($p.ExitCode)" -ForegroundColor Red
            }
            if (-not $containsOk) {
                Write-Host "    output missing required content" -ForegroundColor Red
            }
            if (-not $notContainOk) {
                Write-Host "    output contains unexpected content" -ForegroundColor Red
            }
            if ($all.Length -gt 200) {
                Write-Host "    output (first 200 chars): $($all.Substring(0, 200))" -ForegroundColor DarkGray
            } else {
                Write-Host "    output: $all" -ForegroundColor DarkGray
            }
            $global:failed++
        }
    } catch {
        Write-Host "ERROR: $_" -ForegroundColor Red
        $global:failed++
    }
}

Write-Host ""
Write-Host "========================================" -ForegroundColor Cyan
Write-Host "  filerename CLI Integration Tests" -ForegroundColor Cyan
Write-Host "========================================" -ForegroundColor Cyan
Write-Host ""

# --- Phase 1: Help commands (should exit 0 and show usage) ---
Write-Host "[1] Help commands" -ForegroundColor Yellow

Test-Case -Name "root --help" -CmdArgs @("--help") -MustContain @("filerename", "Usage:", "Available Commands")
Test-Case -Name "list --help" -CmdArgs @("list", "--help") -MustContain @("list", "Usage:")
Test-Case -Name "dedup --help" -CmdArgs @("dedup", "--help") -MustContain @("dedup", "Usage:", "algo")
Test-Case -Name "rename --help" -CmdArgs @("rename", "--help") -MustContain @("rename", "Usage:", "seq")
Test-Case -Name "undo --help" -CmdArgs @("undo", "--help") -MustContain @("undo", "Usage:")
Test-Case -Name "history --help" -CmdArgs @("history", "--help") -MustContain @("history", "Usage:")
Test-Case -Name "config --help" -CmdArgs @("config", "--help") -MustContain @("config", "Usage:")

Write-Host ""

# --- Phase 2: Normal execution (dry-run) ---
Write-Host "[2] Normal execution (dry-run)" -ForegroundColor Yellow

$testDir = Join-Path $env:TEMP "filerename_cli_test"
if (Test-Path $testDir) { Remove-Item -Path $testDir -Recurse -Force }
New-Item -ItemType Directory -Path $testDir -Force | Out-Null

"hello world 12345" | Out-File (Join-Path $testDir "file_a.txt") -Encoding ASCII -NoNewline
"hello world 12345" | Out-File (Join-Path $testDir "file_b.txt") -Encoding ASCII -NoNewline
"different content" | Out-File (Join-Path $testDir "file_c.txt") -Encoding ASCII -NoNewline
"photo sample" | Out-File (Join-Path $testDir "My Photo 001.jpg") -Encoding ASCII -NoNewline
"photo sample2" | Out-File (Join-Path $testDir "My Photo 002.jpg") -Encoding ASCII -NoNewline

Test-Case -Name "list files" -CmdArgs @("list", $testDir) -MustContain @("file_a.txt")
Test-Case -Name "rename dry-run" -CmdArgs @("rename", "--seq", "--dry-run", $testDir) -MustContain @("file_a.txt", "001")
Test-Case -Name "rename verbose steps" -CmdArgs @("rename", "--seq", "--case", "--dry-run", "-v", $testDir) -MustContain @("sequence", "case")
Test-Case -Name "dedup sha256 dry-run" -CmdArgs @("dedup", "--algo", "sha256", "--dry-run", $testDir) -MustContain @("SHA256")
Test-Case -Name "dedup md5 dry-run" -CmdArgs @("dedup", "--algo", "md5", "--dry-run", $testDir) -MustContain @("MD5")
Test-Case -Name "dedup sha1 dry-run" -CmdArgs @("dedup", "--algo", "sha1", "--dry-run", $testDir) -MustContain @("SHA1")

$reportPath = Join-Path $testDir "dup_report.json"
Test-Case -Name "dedup JSON report" -CmdArgs @("dedup", "--algo", "md5", "--dry-run", "--report-json", $reportPath, $testDir) -ExpectedExit 0
if (Test-Path $reportPath) {
    $json = Get-Content $reportPath -Raw | ConvertFrom-Json
    $reportOk = ($json.algorithm -eq "MD5") -and ($json.total_groups -ge 1) -and ($json.groups.Count -ge 1)
    $global:total++
    Write-Host -NoNewline "  [$global:total] dedup report JSON structure ... "
    if ($reportOk) {
        Write-Host "PASS" -ForegroundColor Green
        $global:passed++
    } else {
        Write-Host "FAIL" -ForegroundColor Red
        $global:failed++
    }
}

Test-Case -Name "history empty" -CmdArgs @("history") -ExpectedExit 0

Write-Host ""

# --- Phase 3: Error parameters (exit 2) ---
Write-Host "[3] Error parameters (should exit 2)" -ForegroundColor Yellow

Test-Case -Name "bad regex" -CmdArgs @("list", "--regex", "[", ".") -ExpectedExit 2 -MustContain @("regex")
Test-Case -Name "bad exclude regex" -CmdArgs @("dedup", "--exclude", "(", ".") -ExpectedExit 2 -MustContain @("exclude")
Test-Case -Name "bad pattern" -CmdArgs @("list", "--pattern", "a[b", ".") -ExpectedExit 2 -MustContain @("pattern")
Test-Case -Name "bad hash algo" -CmdArgs @("dedup", "--algo", "xxhash", ".") -ExpectedExit 2 -MustContain @("md5", "sha1", "sha256")
Test-Case -Name "unknown command" -CmdArgs @("xyz") -ExpectedExit 1

Write-Host ""

# --- Cleanup ---
if (Test-Path $testDir) { Remove-Item -Path $testDir -Recurse -Force -ErrorAction SilentlyContinue }

# --- Summary ---
Write-Host "========================================" -ForegroundColor Cyan
Write-Host "  Results: $total tests" -ForegroundColor Cyan
Write-Host -NoNewline "  Passed: $passed " -ForegroundColor Green
Write-Host "Failed: $failed" -ForegroundColor Red
Write-Host "========================================" -ForegroundColor Cyan
Write-Host ""

if ($failed -gt 0) {
    exit 1
} else {
    exit 0
}
