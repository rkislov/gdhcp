# Copyright 2026 Кислов Роман Сергеевич
# SPDX-License-Identifier: Apache-2.0
Set-StrictMode -Version Latest

function Invoke-GodhcpFence {
    param([Parameter(Mandatory)] $Target)
    $ErrorActionPreference = 'Stop'
    if ($Target.restart_disabled -isnot [bool] -or $Target.restart_disabled -ne $true) { throw 'Automatic restart must be disabled' }
    $uuid = [guid]::Parse($Target.uuid)
    if ($Target.provider -eq 'hyperv') {
        Import-Module Hyper-V -ErrorAction Stop
        $vm = Get-VM -Id $uuid -ErrorAction Stop
        if ($vm.IsClustered -or $vm.AutomaticStartAction -ne 'Nothing') {
            throw 'Clustered VM or automatic start is not supported'
        }
        if ($vm.State -ne 'Off') {
            Stop-VM -VM $vm -TurnOff -Confirm:$false -ErrorAction Stop
        }
        do {
            $vm = Get-VM -Id $uuid -ErrorAction Stop
            if ($vm.State -eq 'Off') { return }
            Start-Sleep -Milliseconds 200
        } while ($true)
    }
    if ($Target.provider -eq 'vmware') {
        Import-Module VMware.VimAutomation.Core -ErrorAction Stop
        Set-PowerCLIConfiguration -Scope Session -InvalidCertificateAction Fail -Confirm:$false | Out-Null
        $credential = Import-Clixml -LiteralPath $Target.credential_file -ErrorAction Stop
        if ($credential -isnot [System.Management.Automation.PSCredential]) { throw 'Invalid credential file' }
        $server = Connect-VIServer -Server $Target.server -Credential $credential -NotDefault -ErrorAction Stop
        try {
            $vm = Get-VM -Server $server -Id $Target.vm_id -ErrorAction Stop
            if ([guid]$vm.ExtensionData.Config.Uuid -ne $uuid) { throw 'VM UUID mismatch' }
            $hostState = Get-VMHost -Server $server -VM $vm -ErrorAction Stop
            if ($hostState.ConnectionState -ne 'Connected') { throw 'ESXi not connected' }
            if ($vm.PowerState -ne 'PoweredOff') {
                Stop-VM -VM $vm -Confirm:$false -ErrorAction Stop | Out-Null
            }
            do {
                $vm = Get-VM -Server $server -Id $Target.vm_id -ErrorAction Stop
                $hostState = Get-VMHost -Server $server -VM $vm -ErrorAction Stop
                if ($hostState.ConnectionState -ne 'Connected' -or
                    [guid]$vm.ExtensionData.Config.Uuid -ne $uuid) { throw 'VM identity or host state changed' }
                if ($vm.PowerState -eq 'PoweredOff') { return }
                Start-Sleep -Milliseconds 200
            } while ($true)
        } finally {
            Disconnect-VIServer -Server $server -Confirm:$false -ErrorAction SilentlyContinue | Out-Null
        }
    }
    throw 'Unknown provider'
}
Export-ModuleMember -Function Invoke-GodhcpFence
