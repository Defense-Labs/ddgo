param([Parameter(Mandatory=$true)][string]$DistDir)
$required = @('DDGo.exe','Qt6Core.dll','Qt6Gui.dll','Qt6Widgets.dll','libgcc_s_seh-1.dll','libstdc++-6.dll','libwinpthread-1.dll','platforms/qwindows.dll')
$missing = @(); foreach ($entry in $required) { if (-not (Test-Path (Join-Path $DistDir $entry))) { $missing += $entry } }
if ($missing.Count -gt 0) { Write-Error ("Missing files: " + ($missing -join ', ')); exit 1 }
Write-Host "Windows distribution layout looks valid."
