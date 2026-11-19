# Copyright 2026 Кислов Роман Сергеевич
# SPDX-License-Identifier: Apache-2.0
$ErrorActionPreference = 'Stop'
Import-Module "$PSScriptRoot/../../scripts/GodhcpFencing.psm1" -Force
$module = Get-Module GodhcpFencing
# Provider cmdlets are substituted inside module scope, not on a real hypervisor.
& $module {
    function script:Import-Module { param($Name, $ErrorAction) }
    function script:Get-VM { param($Id, $Server, $ErrorAction); $script:vm }
    function script:Get-VMHost { param($VM, $Server, $ErrorAction); @{ConnectionState=$script:connection} }
    function script:Stop-VM {
        param($VM, [switch]$TurnOff, $Confirm, $ErrorAction)
        $script:stops++
        if ($script:failStop) { throw 'denied' }
        $script:usedTurnOff = $TurnOff.IsPresent
        $script:vm.State = 'Off'
        $script:vm.PowerState = 'PoweredOff'
    }
    function script:Set-PowerCLIConfiguration {
        param($Scope, $InvalidCertificateAction, $Confirm)
        if ($InvalidCertificateAction -ne 'Fail') { throw 'TLS bypass' }
    }
    function script:Import-Clixml {
        param($LiteralPath, $ErrorAction)
        [pscredential]::new('test', (ConvertTo-SecureString 'test' -AsPlainText -Force))
    }
    function script:Connect-VIServer { param($Server, $Credential, [switch]$NotDefault, $ErrorAction); 'server' }
    function script:Disconnect-VIServer { param($Server, $Confirm, $ErrorAction); $script:disconnected = $true }
}
function Reset-Mocks {
    & $module {
        $script:stops = 0; $script:failStop = $false; $script:connection = 'Connected'
        $script:usedTurnOff = $false; $script:disconnected = $false
        $script:vm = [pscustomobject]@{
            State='Running'; PowerState='PoweredOn'; IsClustered=$false; AutomaticStartAction='Nothing'
            ExtensionData=@{Config=@{Uuid='11111111-1111-1111-1111-111111111111'}}
        }
    }
}
function Assert-True($Condition, $Message) { if (-not $Condition) { throw $Message } }
function Expect-Failure($Body) {
    $failed = $false
    try { & $Body } catch { $failed = $true }
    Assert-True $failed 'Expected failure'
}
$tests = 0
foreach ($provider in @('hyperv','vmware')) {
    $target = [pscustomobject]@{
        provider=$provider; restart_disabled=$true; uuid='11111111-1111-1111-1111-111111111111'
        server='vcenter'; vm_id='VirtualMachine-vm-1'; credential_file='test.xml'
    }
    Reset-Mocks
    Invoke-GodhcpFence $target
    Assert-True (& $module { $script:stops -eq 1 }) 'Must stop once'
    if ($provider -eq 'hyperv') { Assert-True (& $module { $script:usedTurnOff }) 'Must use TurnOff' }
    else { Assert-True (& $module { $script:disconnected }) 'Must disconnect' }
    $tests++
    Invoke-GodhcpFence $target
    Assert-True (& $module { $script:stops -eq 1 }) 'Already off must not stop again'
    $tests++
    Reset-Mocks
    & $module { $script:failStop = $true }
    Expect-Failure { Invoke-GodhcpFence $target }
    $tests++
    Reset-Mocks
    $target.restart_disabled = $false
    Expect-Failure { Invoke-GodhcpFence $target }
    Assert-True (& $module { $script:stops -eq 0 }) 'Restart policy must block power action'
    $tests++
    $target.restart_disabled = $true
    if ($provider -eq 'hyperv') {
        & $module { $script:vm.IsClustered = $true }
        Expect-Failure { Invoke-GodhcpFence $target }
        $tests++
        Reset-Mocks
        & $module { $script:vm.AutomaticStartAction = 'Start' }
        Expect-Failure { Invoke-GodhcpFence $target }
        $tests++
    } else {
        & $module { $script:connection = 'Disconnected' }
        Expect-Failure { Invoke-GodhcpFence $target }
        Assert-True (& $module { $script:stops -eq 0 }) 'Disconnected host cannot be fenced by stale inventory'
        $tests++
        Reset-Mocks
        $target.uuid = '22222222-2222-2222-2222-222222222222'
        Expect-Failure { Invoke-GodhcpFence $target }
        Assert-True (& $module { $script:stops -eq 0 }) 'UUID mismatch must block stop'
        $tests++
    }
}
# Parse dispatcher too; no VM modules or credentials are needed for this test.
$tokens = $null; $errors = $null
$null = [System.Management.Automation.Language.Parser]::ParseFile(
    "$PSScriptRoot/../../scripts/godhcp-fence-windows.ps1", [ref]$tokens, [ref]$errors)
Assert-True ($errors.Count -eq 0) 'Dispatcher syntax error'
Write-Output "PASS: $tests provider cases and dispatcher syntax"

# Exercise the actual dispatcher/job boundary with a fake Hyper-V module.
$temp = Join-Path ([System.IO.Path]::GetTempPath()) ('godhcp-fence-test-' + [guid]::NewGuid())
$oldModulePath = $env:PSModulePath
try {
    $null = New-Item -ItemType Directory -Path "$temp/Hyper-V" -Force
    @'
function Get-VM {
    param($Id, $ErrorAction)
    [pscustomobject]@{State='Off'; IsClustered=$false; AutomaticStartAction='Nothing'}
}
function Stop-VM { throw 'Already-off VM must not be stopped' }
Export-ModuleMember -Function Get-VM,Stop-VM
'@ | Set-Content "$temp/Hyper-V/Hyper-V.psm1"
    $env:PSModulePath = $temp + [System.IO.Path]::PathSeparator + $oldModulePath
    $configPath = "$temp/fencing.json"
    @{
        local_node='a'
        targets=@{b=@{provider='hyperv'; restart_disabled=$true; uuid='11111111-1111-1111-1111-111111111111'}}
    } | ConvertTo-Json -Depth 5 | Set-Content $configPath
    $exe = (Get-Process -Id $PID).Path
    $dispatcher = "$PSScriptRoot/../../scripts/godhcp-fence-windows.ps1"
    $result = & $exe -NoProfile -NonInteractive -File $dispatcher -Config $configPath -Node b 2>&1
    Assert-True ($LASTEXITCODE -eq 0 -and "$result" -eq 'FENCED b') 'Dispatcher did not acknowledge Off'
    foreach ($node in @('a', 'unknown', 'b;whoami', "b`n")) {
        $result = & $exe -NoProfile -NonInteractive -File $dispatcher -Config $configPath -Node $node 2>&1
        Assert-True ($LASTEXITCODE -ne 0 -and "$result" -notmatch 'FENCED') 'Dispatcher accepted invalid target'
    }
    Write-Output 'PASS: dispatcher success and 4 rejection cases'
} finally {
    $env:PSModulePath = $oldModulePath
    Remove-Item -LiteralPath $temp -Recurse -Force
}
