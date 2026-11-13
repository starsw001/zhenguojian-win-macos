param(
    [switch]$ChinaMirrors,
    [switch]$AllSources,
    [switch]$Run,
    [switch]$DisableRemoteImages,
    [switch]$Archive
)
$ErrorActionPreference = "Stop"
$BuildArguments = @()
if ($ChinaMirrors) { $BuildArguments += "--cn-mirrors" }
if ($AllSources) { $BuildArguments += "--all-sources" }
if ($Run) { $BuildArguments += "--run" }
if ($DisableRemoteImages) { $BuildArguments += "--disable-remote-images" }
if ($Archive) { $BuildArguments += "--archive" }
python (Join-Path $PSScriptRoot "build_windows.py") @BuildArguments
exit $LASTEXITCODE
