<#
.SYNOPSIS
  Registra o synk para subir sozinho no logon deste PC Windows.

.DESCRIPTION
  Cria uma tarefa agendada que inicia `synk.exe agent` quando você
  entra na sua conta, e a reinicia se o processo morrer.

  Copia o executável achado (ou passado em -ExePath) para
  `%USERPROFILE%\.synk\bin\synk.exe` e põe essa pasta no PATH do usuário, para
  que `synk` funcione em qualquer terminal novo e a tarefa agendada não fique
  presa a um caminho que você depois vai apagar (Desktop, pasta do instalador
  do Tauri, etc).

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
  Roda sem janela de console: a tarefa chama `synk-agent.exe` (o mesmo agente,
  compilado sem console) em vez de `synk.exe`, e ele escreve num `synk.log` ao
  lado de si mesmo. Sem `synk-agent.exe` ao lado do executável passado, cai de
  volta num PowerShell escondido por cima do `synk.exe` normal, que funciona
  mas pisca um console vazio por um instante ao iniciar. Sem -Hidden, uma
  janela de console fica aberta — feia, mas é onde as mensagens aparecem.

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

# synk-agent.exe e o mesmo agente, compilado sem console (-ldflags
# -H=windowsgui): e o que a tarefa escondida roda, porque um exe de console
# ganha uma janela do Windows no instante em que e criado, antes de qualquer
# '-WindowStyle Hidden' ter chance de agir. Procurado do lado do synk.exe
# encontrado, opcional: sem ele, -Hidden volta ao truque antigo (PowerShell
# escondido por cima do console), que ainda pisca ao abrir.
$exeAgente = Join-Path (Split-Path $exe -Parent) 'synk-agent.exe'
$exeAgente = (Resolve-Path -LiteralPath $exeAgente -ErrorAction SilentlyContinue).Path

# Pousa sempre no mesmo lugar, fora da pasta de onde veio (Desktop, Taildrop,
# a pasta do instalador): assim "synk" funciona em qualquer terminal, e
# reinstalar o app (que roda este script de novo, de outro caminho) atualiza
# o mesmo arquivo em vez de deixar uma copia antiga presa na Scheduled Task.
$destino = Join-Path $env:USERPROFILE '.synk\bin'
$exeDestino = Join-Path $destino 'synk.exe'
$exeAgenteDestino = Join-Path $destino 'synk-agent.exe'
if ($exe -ne (Resolve-Path -LiteralPath $exeDestino -ErrorAction SilentlyContinue).Path) {
    # Uma atualizacao por cima de um agente de pe trava o arquivo; para a
    # tarefa, e tambem qualquer processo solto rodando direto desse caminho
    # (um 'synk up' ou um agent iniciado a mao, que Stop-ScheduledTask nao
    # enxerga).
    Stop-ScheduledTask -TaskName $TaskName -ErrorAction SilentlyContinue
    Get-Process -ErrorAction SilentlyContinue |
        Where-Object { $_.Path -eq $exeDestino -or $_.Path -eq $exeAgenteDestino } |
        Stop-Process -Force -ErrorAction SilentlyContinue
    New-Item -ItemType Directory -Force -Path $destino | Out-Null
    # Stop-ScheduledTask mata o processo mas nao espera o handle do arquivo
    # soltar; tenta de novo por alguns segundos antes de desistir.
    for ($tentativa = 1; $true; $tentativa++) {
        try {
            Copy-Item -LiteralPath $exe -Destination $exeDestino -Force
            if ($exeAgente) { Copy-Item -LiteralPath $exeAgente -Destination $exeAgenteDestino -Force }
            break
        } catch {
            if ($tentativa -ge 10) { throw }
            Start-Sleep -Milliseconds 500
        }
    }
    Write-Host "copiado para: $exeDestino"
    $exe = $exeDestino
    if ($exeAgente) {
        $exeAgente = $exeAgenteDestino
        Write-Host "copiado para: $exeAgente"
    }
}

$pathAtual = [Environment]::GetEnvironmentVariable('Path', 'User')
if (-not ($pathAtual -split ';' | Where-Object { $_ -eq $destino })) {
    $novoPath = if ($pathAtual) { "$pathAtual;$destino" } else { $destino }
    [Environment]::SetEnvironmentVariable('Path', $novoPath, 'User')
    Write-Host "adicionado ao PATH do usuario: $destino (terminais abertos precisam reabrir)"
}

$log = Join-Path (Split-Path $exe -Parent) 'synk.log'
if ($Hidden -and $exeAgente) {
    # synk-agent.exe nao tem console pra ganhar uma janela; ele mesmo escreve
    # em synk.log (ve cmd/synk-agent), entao nao precisa de PowerShell por
    # cima nem de redirecionamento de shell.
    $action = New-ScheduledTaskAction -Execute $exeAgente
    Write-Host "log: $log"
} elseif ($Hidden) {
    # Sem synk-agent.exe (compilacao so com 'make build-windows', sem o
    # ldflags -H=windowsgui): volta ao truque antigo, que funciona mas pisca
    # um console vazio por um instante ao iniciar.
    Write-Host "aviso: synk-agent.exe nao encontrado; -Hidden vai sem ele e pode piscar um console ao iniciar."
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
