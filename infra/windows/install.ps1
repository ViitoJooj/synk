<#
.SYNOPSIS
  Registra o synk para subir sozinho no logon deste PC Windows.

.DESCRIPTION
  Cria uma tarefa agendada que inicia `synk.exe agent` quando você
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
  Caminho do synk.exe. Sem ele, o script procura em ordem: ao lado deste
  arquivo (é onde os dois caem quando você os manda por Taildrop) e depois na
  raiz do repositório, dois níveis acima.

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
  .\install.ps1 -ExePath C:\Tools\synk.exe -Hidden

.NOTES
  Para desfazer:  Unregister-ScheduledTask -TaskName synk -Confirm:$false
  Para ver:       Get-ScheduledTask -TaskName synk | Get-ScheduledTaskInfo
  Para rodar já:  Start-ScheduledTask -TaskName synk   (ou: synk up)
#>
[CmdletBinding()]
param(
    [string] $ExePath,
    [string] $TaskName  = 'synk',
    [switch] $Hidden,
    [switch] $Elevated
)

$ErrorActionPreference = 'Stop'

# Onde procurar, em ordem. O primeiro caso e o que acontece de verdade quando
# os dois arquivos chegam juntos por Taildrop: eles ficam LADO A LADO, e nao no
# layout do repositorio.
$candidatos = if ($ExePath) {
    @($ExePath)
} else {
    @(
        (Join-Path $PSScriptRoot 'synk.exe'),
        (Join-Path $PSScriptRoot '..\..\synk.exe'),
        (Join-Path (Get-Location) 'synk.exe')
    )
}

$exe = $null
foreach ($c in $candidatos) {
    $r = Resolve-Path -LiteralPath $c -ErrorAction SilentlyContinue
    if ($r) { $exe = $r.Path; break }
}

if (-not $exe) {
    $tentados = ($candidatos | ForEach-Object { "  $_" }) -join "`n"
    throw "synk.exe nao encontrado. Procurei em:`n$tentados`n" +
          "Passe o caminho em -ExePath, ou compile com " +
          "'GOOS=windows go build -o synk.exe ./cmd/synk'."
}
Write-Host "executavel: $exe"

if ($Hidden) {
    # PowerShell escondido no lugar do console. A saida vai para um arquivo,
    # senao ela se perde por completo — e e nela que aparece por que o link
    # esta fora do ar.
    $log = Join-Path (Split-Path $exe -Parent) 'synk.log'
    $inner = "& '$exe' agent *> '$log'"
    $action = New-ScheduledTaskAction -Execute 'powershell.exe' `
        -Argument "-NoProfile -NonInteractive -WindowStyle Hidden -Command `"$inner`""
    Write-Host "log: $log"
} else {
    $action = New-ScheduledTaskAction -Execute $exe -Argument 'agent'
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

# Nomes antigos (wfkit-link ate 2026-09-24, conn ate 2026-09-25): a tarefa
# velha subiria o exe velho junto com o novo, e os dois capturariam o mesmo
# teclado. Para antes de remover, senao o processo velho continua de pe.
foreach ($velha in @('wfkit-link', 'conn')) {
    if ($velha -eq $TaskName) { continue }
    if (Get-ScheduledTask -TaskName $velha -ErrorAction SilentlyContinue) {
        Stop-ScheduledTask -TaskName $velha -ErrorAction SilentlyContinue
        Unregister-ScheduledTask -TaskName $velha -Confirm:$false
        Write-Host "tarefa antiga '$velha' removida."
    }
}

Register-ScheduledTask -TaskName $TaskName -Action $action -Trigger $trigger `
    -Settings $settings -Principal $principal -Force `
    -Description 'Conecta este PC ao notebook Linux (synk).' | Out-Null

Write-Host "tarefa '$TaskName' registrada, nivel $nivel."
Write-Host ""
Write-Host "Iniciar agora sem deslogar:  Start-ScheduledTask -TaskName $TaskName"
Write-Host "Conferir:                    Get-ScheduledTaskInfo -TaskName $TaskName"
Write-Host "Remover:                     Unregister-ScheduledTask -TaskName $TaskName -Confirm:`$false"
