[CmdletBinding()]
param(
  [ValidateSet('hongguoshang', 'zhenguoshang', 'both')]
  [string]$Edition = 'both',
  [ValidateRange(1, 60)]
  [int]$TimeoutSeconds = 15
)

$ErrorActionPreference = 'Stop'
Add-Type @"
using System;
using System.Runtime.InteropServices;

public static class GuoClientThreadTerminator {
  [DllImport("kernel32.dll", SetLastError = true)]
  private static extern IntPtr OpenThread(uint access, bool inheritHandle, uint threadId);

  [DllImport("kernel32.dll", SetLastError = true)]
  private static extern bool TerminateThread(IntPtr thread, uint exitCode);

  [DllImport("kernel32.dll")]
  private static extern bool CloseHandle(IntPtr handle);

  public static int Terminate(uint threadId) {
    IntPtr thread = OpenThread(1, false, threadId);
    if (thread == IntPtr.Zero) {
      return Marshal.GetLastWin32Error();
    }
    try {
      return TerminateThread(thread, 0) ? 0 : Marshal.GetLastWin32Error();
    } finally {
      CloseHandle(thread);
    }
  }
}
"@
$names = switch ($Edition) {
  'hongguoshang' { @('hongguoshang.exe') }
  'zhenguoshang' { @('zhenguoshang.exe') }
  default { @('hongguoshang.exe', 'zhenguoshang.exe') }
}

function Get-GuoClientProcesses {
  @(Get-CimInstance Win32_Process | Where-Object { $_.Name -in $names })
}

function Stop-GuoClientProcess($item) {
  $process = Get-Process -Id $item.ProcessId -ErrorAction SilentlyContinue
  if ($null -ne $process) {
    try {
      $process.Kill($true)
      [void]$process.WaitForExit(2000)
    } catch {}
  }
  try {
    [void](Invoke-CimMethod -InputObject $item -MethodName Terminate -Arguments @{Reason = 0})
  } catch {}
  $current = Get-CimInstance Win32_Process -Filter "ProcessId=$($item.ProcessId)" -ErrorAction SilentlyContinue
  if ($null -eq $current) {
    return
  }
  $threads = @(Get-CimInstance Win32_Thread -Filter "ProcessHandle='$($item.ProcessId)'" -ErrorAction SilentlyContinue)
  foreach ($thread in $threads) {
    $result = [GuoClientThreadTerminator]::Terminate([uint32]$thread.Handle)
    Write-Output "STOP_CLIENT_THREAD pid=$($item.ProcessId) tid=$($thread.Handle) result=$result"
  }
}

$before = @(Get-GuoClientProcesses)
Write-Output "CLIENT_PROCESS_BEFORE=$($before.Count)"
foreach ($item in $before) {
  Write-Output "STOP_CLIENT_PROCESS name=$($item.Name) pid=$($item.ProcessId) parent=$($item.ParentProcessId) path=$($item.ExecutablePath)"
  Stop-GuoClientProcess $item
}

$deadline = (Get-Date).AddSeconds($TimeoutSeconds)
do {
  Start-Sleep -Milliseconds 200
  $remaining = @(Get-GuoClientProcesses)
  foreach ($item in $remaining) {
    Stop-GuoClientProcess $item
  }
} while ($remaining.Count -gt 0 -and (Get-Date) -lt $deadline)

Write-Output "CLIENT_PROCESS_AFTER=$($remaining.Count)"
if ($remaining.Count -gt 0) {
  $details = $remaining | ForEach-Object { "$($_.Name):$($_.ProcessId):$($_.ExecutablePath)" }
  throw "客户端进程未清零：$($details -join ', ')"
}
