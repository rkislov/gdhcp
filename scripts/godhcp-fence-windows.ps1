# Copyright 2026 Кислов Роман Сергеевич
# SPDX-License-Identifier: Apache-2.0
param(
    [string]$Config = 'C:\ProgramData\GoDHCP\fencing.json',
    [string]$Node = $env:SSH_ORIGINAL_COMMAND
)
$ErrorActionPreference = 'Stop'
$job = $null
try {
    if ($Node -cnotmatch '^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}\z') { throw 'Invalid node-id' }
    $job = Start-Job -ArgumentList $Config, $Node, "$PSScriptRoot\GodhcpFencing.psm1" -ScriptBlock {
        param($path, $nodeId, $modulePath)
        $ErrorActionPreference = 'Stop'
        Set-StrictMode -Version Latest
        Import-Module $modulePath -Force
        $config = Get-Content -LiteralPath $path -Raw | ConvertFrom-Json
        if (-not $config.local_node -or $config.local_node -eq $nodeId) { throw 'Invalid local node or self-fencing' }
        $property = $config.targets.PSObject.Properties[$nodeId]
        if ($null -eq $property -or $property.Name -cne $nodeId) { throw 'Unknown node-id' }
        Invoke-GodhcpFence -Target $property.Value
        'FENCED ' + $nodeId
    }
    $null = Wait-Job -Job $job -Timeout 17
    if ($job.State -ne 'Completed') { throw 'Provider failed or timed out' }
    $result = @(Receive-Job -Job $job -ErrorAction Stop)
    if ($result.Count -ne 1 -or $result[0] -cne ('FENCED ' + $Node)) { throw 'Missing acknowledgement' }
    Write-Output $result[0]
    exit 0
} catch {
    [Console]::Error.WriteLine('fencing failed: check target, management access and provider state')
    exit 1
} finally {
    if ($null -ne $job) { Remove-Job -Job $job -Force -ErrorAction SilentlyContinue }
}
