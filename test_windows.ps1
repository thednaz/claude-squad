# test_windows.ps1 — Windows Compatibility Test Runner for Claude Squad
#
# Prerequisites:
#   1. Go 1.23+ installed
#   2. Git installed
#   3. psmux installed (scoop install psmux, or cargo install psmux)
#   4. Verify "tmux" alias works: tmux -V
#
# Usage:
#   .\test_windows.ps1           # Run all tests
#   .\test_windows.ps1 -Quick    # Quick prereq check only
#   .\test_windows.ps1 -Verbose  # Verbose output

param(
    [switch]$Quick,
    [switch]$Verbose
)

$ErrorActionPreference = "Stop"
$script:passed = 0
$script:failed = 0
$script:skipped = 0

function Write-Status($icon, $msg) {
    Write-Host "$icon $msg"
}

function Test-Pass($name) {
    $script:passed++
    Write-Status "[PASS]" $name
}

function Test-Fail($name, $detail) {
    $script:failed++
    Write-Status "[FAIL]" "$name — $detail"
}

function Test-Skip($name, $reason) {
    $script:skipped++
    Write-Status "[SKIP]" "$name — $reason"
}

# ============================================================================
# Phase 1: Prerequisites
# ============================================================================
Write-Host "`n=== Phase 1: Prerequisites ===" -ForegroundColor Cyan

# 1.1 Go
try {
    $goVer = go version 2>&1
    Test-Pass "Go installed: $goVer"
} catch {
    Test-Fail "Go installed" "go not found on PATH"
}

# 1.2 Git
try {
    $gitVer = git --version 2>&1
    Test-Pass "Git installed: $gitVer"
} catch {
    Test-Fail "Git installed" "git not found on PATH"
}

# 1.3 psmux / tmux alias
$psmuxAvailable = $false
try {
    $tmuxPath = (Get-Command tmux -ErrorAction Stop).Source
    $tmuxVer = tmux -V 2>&1
    Test-Pass "psmux installed: $tmuxVer (at $tmuxPath)"
    $psmuxAvailable = $true
} catch {
    Test-Fail "psmux installed" "tmux not found — install with: scoop install psmux"
}

# 1.4 Windows version (ConPTY requires 1809+)
$build = [System.Environment]::OSVersion.Version.Build
if ($build -ge 17763) {
    Test-Pass "Windows build $build (ConPTY supported, requires 17763+)"
} else {
    Test-Fail "Windows build" "Build $build < 17763 — ConPTY not available"
}

# 1.5 COMSPEC
$comspec = $env:COMSPEC
if ($comspec) {
    Test-Pass "COMSPEC set: $comspec"
} else {
    Test-Fail "COMSPEC" "not set"
}

# 1.6 PowerShell
try {
    $psVer = $PSVersionTable.PSVersion
    Test-Pass "PowerShell $psVer"
} catch {
    Test-Skip "PowerShell version" "could not detect"
}

if ($Quick) {
    Write-Host "`nQuick check complete." -ForegroundColor Cyan
    exit 0
}

# ============================================================================
# Phase 2: Build
# ============================================================================
Write-Host "`n=== Phase 2: Build ===" -ForegroundColor Cyan

# 2.1 Build all packages
try {
    $buildOut = go build ./... 2>&1
    if ($LASTEXITCODE -eq 0) {
        Test-Pass "go build ./..."
    } else {
        Test-Fail "go build ./..." "$buildOut"
    }
} catch {
    Test-Fail "go build ./..." $_.Exception.Message
}

# 2.2 go vet
try {
    $vetOut = go vet ./... 2>&1
    if ($LASTEXITCODE -eq 0) {
        Test-Pass "go vet ./..."
    } else {
        Test-Fail "go vet ./..." "$vetOut"
    }
} catch {
    Test-Fail "go vet ./..." $_.Exception.Message
}

# ============================================================================
# Phase 3: Unit Tests (no psmux required)
# ============================================================================
Write-Host "`n=== Phase 3: Unit Tests ===" -ForegroundColor Cyan

# 3.1 Config tests
try {
    $out = go test ./config/ -v 2>&1
    if ($LASTEXITCODE -eq 0) { Test-Pass "config tests" } else { Test-Fail "config tests" "$out" }
} catch {
    Test-Fail "config tests" $_.Exception.Message
}

# 3.2 Tmux package tests (mock-based)
try {
    $out = go test ./session/tmux/ -v -run "TestSanitize|TestStartTmuxSession" 2>&1
    if ($LASTEXITCODE -eq 0) { Test-Pass "tmux mock tests" } else { Test-Fail "tmux mock tests" "$out" }
} catch {
    Test-Fail "tmux mock tests" $_.Exception.Message
}

# 3.3 Git util tests
try {
    $out = go test ./session/git/ -v 2>&1
    if ($LASTEXITCODE -eq 0) { Test-Pass "git util tests" } else { Test-Fail "git util tests" "$out" }
} catch {
    Test-Fail "git util tests" $_.Exception.Message
}

# 3.4 App tests
try {
    $out = go test ./app/ -v 2>&1
    if ($LASTEXITCODE -eq 0) { Test-Pass "app tests" } else { Test-Fail "app tests" "$out" }
} catch {
    Test-Fail "app tests" $_.Exception.Message
}

# ============================================================================
# Phase 4: ConPTY Tests (no psmux required, but needs Windows)
# ============================================================================
Write-Host "`n=== Phase 4: ConPTY Tests ===" -ForegroundColor Cyan

try {
    if ($Verbose) {
        $out = go test ./session/tmux/ -v -run "TestWindowsConPty" -timeout 30s 2>&1
    } else {
        $out = go test ./session/tmux/ -run "TestWindowsConPty" -timeout 30s 2>&1
    }
    if ($LASTEXITCODE -eq 0) {
        Test-Pass "ConPTY create/close/resize/read-write/EOF"
    } else {
        Test-Fail "ConPTY tests" "$out"
    }
} catch {
    Test-Fail "ConPTY tests" $_.Exception.Message
}

# ============================================================================
# Phase 5: psmux Integration Tests (require psmux)
# ============================================================================
Write-Host "`n=== Phase 5: psmux Integration Tests ===" -ForegroundColor Cyan

if (-not $psmuxAvailable) {
    Test-Skip "psmux integration" "psmux not installed"
} else {
    # 5.1 Session lifecycle
    try {
        if ($Verbose) {
            $out = go test ./session/tmux/ -v -run "TestWindowsPsmux" -timeout 60s 2>&1
        } else {
            $out = go test ./session/tmux/ -run "TestWindowsPsmux" -timeout 60s 2>&1
        }
        if ($LASTEXITCODE -eq 0) {
            Test-Pass "psmux session lifecycle / send-keys / ANSI / cleanup"
        } else {
            Test-Fail "psmux integration tests" "$out"
        }
    } catch {
        Test-Fail "psmux integration tests" $_.Exception.Message
    }
}

# ============================================================================
# Phase 6: Shell Detection
# ============================================================================
Write-Host "`n=== Phase 6: Shell Detection ===" -ForegroundColor Cyan

try {
    $out = go test ./session/tmux/ -v -run "TestWindowsDefaultShell" -timeout 10s 2>&1
    if ($LASTEXITCODE -eq 0) { Test-Pass "shell detection" } else { Test-Fail "shell detection" "$out" }
} catch {
    Test-Fail "shell detection" $_.Exception.Message
}

# Also test config.DefaultShell and config.GetClaudeCommand
try {
    $out = go test ./config/ -v -run "TestGetClaudeCommand" -timeout 10s 2>&1
    if ($LASTEXITCODE -eq 0) { Test-Pass "GetClaudeCommand" } else { Test-Skip "GetClaudeCommand" "claude not installed (expected)" }
} catch {
    Test-Skip "GetClaudeCommand" "claude not installed (expected)"
}

# ============================================================================
# Phase 7: Manual Verification Prompts
# ============================================================================
Write-Host "`n=== Phase 7: Manual Checks ===" -ForegroundColor Yellow
Write-Host @"

The following must be verified manually:

  [ ] Build and run: go run . --help
  [ ] Start a session: go run . (does the TUI render correctly?)
  [ ] Attach to session (Ctrl+Q to detach)
  [ ] Preview pane shows content with ANSI colors
  [ ] Window resize works (drag terminal, check pane reflows)
  [ ] Pause/resume a session
  [ ] Kill a session
  [ ] git worktree created under ~/.claude-squad/worktrees/
  [ ] Config file written to ~/.claude-squad/config.json
  [ ] Daemon mode: go run . --daemon

"@

# ============================================================================
# Summary
# ============================================================================
Write-Host "=== Summary ===" -ForegroundColor Cyan
Write-Host "  Passed:  $script:passed" -ForegroundColor Green
Write-Host "  Failed:  $script:failed" -ForegroundColor $(if ($script:failed -gt 0) { "Red" } else { "Green" })
Write-Host "  Skipped: $script:skipped" -ForegroundColor Yellow
Write-Host ""

if ($script:failed -gt 0) {
    Write-Host "SOME TESTS FAILED" -ForegroundColor Red
    exit 1
} else {
    Write-Host "ALL AUTOMATED TESTS PASSED" -ForegroundColor Green
    exit 0
}
