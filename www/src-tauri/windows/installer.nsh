; Hooked into the generated installer.nsi (see tauri.windows.conf.json ->
; bundle.windows.nsis.installerHooks). This is what makes installing the
; window also install the agent: without it the installer only drops
; synk-ui.exe, and 'synk up' / the window have nothing to start.
;
; synk.exe and install.ps1 land in $INSTDIR\resources (see
; tauri.windows.conf.json -> bundle.resources) before this hook runs.

!include "LogicLib.nsh"

!macro NSIS_HOOK_POSTINSTALL
  DetailPrint "Instalando o agente synk (tarefa agendada)..."
  ExecWait '"$SYSDIR\WindowsPowerShell\v1.0\powershell.exe" -NoProfile -ExecutionPolicy Bypass -File "$INSTDIR\resources\install.ps1" -ExePath "$INSTDIR\resources\synk.exe" -Hidden' $0
  ${If} $0 != 0
    DetailPrint "install.ps1 saiu com codigo $0; synk up/synk doctor explicam o que falta."
  ${EndIf}
!macroend

!macro NSIS_HOOK_PREUNINSTALL
  DetailPrint "Removendo a tarefa agendada do synk..."
  ExecWait '"$SYSDIR\WindowsPowerShell\v1.0\powershell.exe" -NoProfile -ExecutionPolicy Bypass -Command "Stop-ScheduledTask -TaskName synk -ErrorAction SilentlyContinue; Unregister-ScheduledTask -TaskName synk -Confirm:$$false -ErrorAction SilentlyContinue"'
!macroend
