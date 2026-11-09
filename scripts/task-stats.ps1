# ============================================================================
# task-stats.ps1
# Recount actual task statuses in docs/tasks.json for implementation agents
# updating meta.taskCountByStatus; keep counts consistent with status fields.
# Usage: powershell -File scripts/task-stats.ps1
# ============================================================================
$ErrorActionPreference = "Stop"

# Locate the repository root; this script lives in <root>/scripts.
$root = Split-Path -Parent (Split-Path -Parent $MyInvocation.MyCommand.Path)
$jsonPath = Join-Path $root "docs\tasks.json"

$json = Get-Content -Path $jsonPath -Raw -Encoding UTF8 | ConvertFrom-Json

# Initialize status counters.
$counts = @{ pending = 0; in_progress = 0; completed = 0; blocked = 0; deferred = 0; skipped = 0 }

foreach ($t in $json.tasks) {
    $s = $t.status
    if (-not $counts.ContainsKey($s)) { $counts[$s] = 0 }
    $counts[$s]++
}

Write-Host "=== docs/tasks.json 任务状态统计 ==="
Write-Host ("total      : {0}" -f $json.tasks.Count)
Write-Host ("pending    : {0}" -f $counts['pending'])
Write-Host ("in_progress: {0}" -f $counts['in_progress'])
Write-Host ("completed  : {0}" -f $counts['completed'])
Write-Host ("blocked    : {0}" -f $counts['blocked'])
Write-Host ("deferred   : {0}" -f $counts['deferred'])
Write-Host ("skipped    : {0}" -f $counts['skipped'])
