<#
.SYNOPSIS
  Registra o wfkit-link para subir sozinho no logon deste PC Windows.

.DESCRIPTION
  Cria uma tarefa agendada que inicia `wfkit-link.exe session` quando você
  entra na sua conta, e a reinicia se o processo morrer.

  Por que no LOGON e não no boot: este lado captura teclado e mouse com hooks
  de baixo nível do Windows, que só funcionam dentro de uma sessão interativa.
  Uma tarefa "rodar mesmo sem o usuário logado" não teria desktop para
  observar — e ainda exigiria guardar sua senha.

  Por que sem privilégio elevado: capturar entrada comum não precisa. Elevar
  daria à tarefa poder que ela não usa. (O efeito colateral conhecido é que
  janelas executando como administrador não entregam entrada a um processo não
  elevado — se isso incomodar, use -Elevated, ciente do que está trocando.)

  O tailscale NÃO precisa estar pronto quando isto sobe. O binário espera:
  ele consulta o backend e o estado do peer, recua de 1s a 30s e só reporta
  quando o motivo muda.

.PARAMETER ExePath
  Caminho do wfkit-link.exe. Padrão: o .exe ao lado deste script.

.PARAMETER Hidden
  Roda sem janela de console, redirecionando a saída para um arquivo de log ao
  lado do executável. Sem isto, uma janela de console fica aberta — feia, mas
  é onde as mensagens aparecem.

.PARAMETER Elevated
  Roda com privilégio máximo. Só se você precisar que o link funcione sobre
  janelas de administrador.

.EXAMPLE
  .\install.ps1
  .\install.ps1 -Hidden
  .\install.ps1 -ExePath C:\Tools\wfkit-link.exe -Hidden

.NOTES
  Para desfazer:  Unregister-ScheduledTask -TaskName wfkit-link -Confirm:$false
  Para ver:       Get-ScheduledTask -TaskName wfkit-link | Get-ScheduledTaskInfo
  Para rodar já:  Start-ScheduledTask -TaskName wfkit-link
#>
[CmdletBinding()]
param(
    [string] $ExePath   = (Join-Path $PSScriptRoot '..\..\wfkit-link.exe'),
    [string] $TaskName  = 'wfkit-link',
    [switch] $Hidden,
    [switch] $Elevated
)

$ErrorActionPreference = 'Stop'

$exe = (Resolve-Path -LiteralPath $ExePath -ErrorAction SilentlyContinue)
if (-not $exe) {
    throw "wfkit-link.exe nao encontrado em '$ExePath'. " +
          "Compile com 'GOOS=windows go build -o wfkit-link.exe ./cmd/link' " +
          "e passe o caminho em -ExePath."
}
$exe = $exe.Path
Write-Host "executavel: $exe"

if ($Hidden) {
    # PowerShell escondido no lugar do console. A saida vai para um arquivo,
    # senao ela se perde por completo — e e nela que aparece por que o link
    # esta fora do ar.
    $log = Join-Path (Split-Path $exe -Parent) 'wfkit-link.log'
    $inner = "& '$exe' session *> '$log'"
    $action = New-ScheduledTaskAction -Execute 'powershell.exe' `
        -Argument "-NoProfile -NonInteractive -WindowStyle Hidden -Command `"$inner`""
    Write-Host "log: $log"
} else {
    $action = New-ScheduledTaskAction -Execute $exe -Argument 'session'
}

# Atraso curto: nao e necessario (o binario espera o tailscale sozinho), mas
# evita uma rajada de tentativas enquanto a rede do Windows ainda sobe.
# Nome completo, nao so o usuario: numa maquina em dominio o nome curto pode
# nao resolver, e a tarefa e registrada para a conta errada ou falha.
$conta = "$env:USERDOMAIN\$env:USERNAME"
Write-Host "conta: $conta"

$trigger = New-ScheduledTaskTrigger -AtLogOn -User $conta
$trigger.Delay = 'PT20S'

$settings = New-ScheduledTaskSettingsSet `
    -AllowStartIfOnBatteries `
    -DontStopIfGoingOnBatteries `
    -DontStopOnIdleEnd `
    -StartWhenAvailable `
    -RestartInterval (New-TimeSpan -Minutes 1) `
    -RestartCount 3 `
    -MultipleInstances IgnoreNew `
    -ExecutionTimeLimit ([TimeSpan]::Zero)

$nivel = if ($Elevated) { 'Highest' } else { 'Limited' }
$principal = New-ScheduledTaskPrincipal -UserId $conta `
    -LogonType Interactive -RunLevel $nivel

Register-ScheduledTask -TaskName $TaskName -Action $action -Trigger $trigger `
    -Settings $settings -Principal $principal -Force `
    -Description 'Conecta este PC ao notebook Linux (wfkit-link).' | Out-Null

Write-Host "tarefa '$TaskName' registrada, nivel $nivel."
Write-Host ""
Write-Host "Iniciar agora sem deslogar:  Start-ScheduledTask -TaskName $TaskName"
Write-Host "Conferir:                    Get-ScheduledTaskInfo -TaskName $TaskName"
Write-Host "Remover:                     Unregister-ScheduledTask -TaskName $TaskName -Confirm:`$false"
