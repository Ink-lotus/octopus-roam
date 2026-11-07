# Developer-only test entry. Native tests use isolated runtime and task fixtures.
Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'
Push-Location -LiteralPath (Join-Path $PSScriptRoot '..\..')
try {
    & go test ./internal/desktop -run 'TestTask|TestScheduler'
    if ($LASTEXITCODE -ne 0) { throw 'Native Windows launcher tests failed.' }
} finally { Pop-Location }
