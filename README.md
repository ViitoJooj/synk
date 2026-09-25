# synk

Compartilha mouse, teclado, área de transferência e áudio entre uma máquina
Linux e um PC Windows na mesma tailnet. O ponteiro atravessa a borda da tela e
o teclado vai junto; não há agente na nuvem e não há servidor no meio.

Extraído de [wfkit](https://github.com/ViitoJooj/wfkit) em 2026-09-05, onde
vivia como subcomando do CLI. Virou projeto próprio porque é um app de duas
máquinas com ciclo de vida próprio, não parte do provisionamento de um desktop.
Chamou-se `wfkit-link` até 2026-09-24 e `conn` até 2026-09-25;
`infra/install.sh` e `infra/windows/install.ps1` migram uma instalação com o
nome `wfkit-link`. As units, o grupo e a config ainda usam o nome `conn`.

## Como está montado

```
cmd/synk/          o binário: só chama internal/cli
internal/cli/      os comandos, um arquivo por comando
internal/agent/    o processo que fica de pé: link, clipboard, áudio
internal/injector/ o daemon root que abre /dev/uinput
internal/service/  liga, desliga e lê o estado das units e da tarefa agendada
internal/          transporte, protocolo, entrada, áudio, clipboard, foco
pkg/errorx/        os erros com código e dica (snk-NN), no formato do devxx
infra/             units de systemd, install.sh e install.ps1
```

## Comandos

```
synk <peer>        passa a ligar com essa máquina e reinicia o agente. Aceita o
                   nome ou o SO: "synk windows" escolhe o único Windows online
synk ls            as máquinas da tailnet; * marca a atual
synk status        agente, injetor, peer e ajustes numa tela
synk doctor        confere tudo de que o link depende e diz como consertar
synk up | down | restart
synk logs [-f] [-n 50] [--injector]
```

Flags globais: `--json` (para scripts), `-q`, `--peer`, `--port`. Os erros
saem com código, como no devxx:

```
code: snk-01
error: more than one macos machine is online (mac, mac-old)
error_type: user error.
message: name the one you want: synk mac
```

`synk agent` e `synk injector` são o que o systemd e a tarefa agendada rodam,
e ficam fora da ajuda. Os nomes antigos, `session` e `daemon`, continuam
aceitos para as units já instaladas.

A direção não muda com `synk <peer>`: o Linux continua escutando e o Windows
continua discando. No Linux o peer é gravado no `conn.yaml`, trocando só a
linha `peer:`. No Windows, que não lê config de propósito, fica num arquivo
`peer` com um nome só, em `%APPDATA%\conn\`.

Dois processos no Linux, de propósito:

- **`synk injector`** (root, unit de sistema) abre `/dev/uinput` e não faz
  mais nada. Sem rede — `PrivateNetwork=yes` —, sem áudio, sem clipboard. Ele
  injeta entrada e nunca lê. O único canal é um socket unix.
- **`synk agent`** (seu usuário, unit de sessão) tem a conexão com o
  peer, o clipboard e o áudio. Para digitar, pede ao daemon pelo socket. Não
  detém privilégio nenhum.

A separação existe porque quem pode digitar por você não deve poder falar com a
rede.

## Direção da conexão

**Hoje: o Linux escuta e o Windows disca.** Não é arbitrário — um listener no
Windows exige regra de firewall aprovada por administrador, e sem ela a conexão
falha com `i/o timeout`, que é a assinatura de pacote descartado e não de porta
fechada.

Autenticação é o IP de origem do peer na tailnet, que o WireGuard torna
inforjável entre nós. Não há chave pré-compartilhada e não há comando `pair`.
O lado Windows não lê arquivo de configuração nenhum: descobre o peer pela
tailnet e a borda de travessia pelo hello.

## Medições que sustentam o código

Cada uma foi um bug antes de virar constante. Mudar qualquer coisa perto delas
sem reler custa tempo.

- **libinput acelera movimento injetado ~1.87x** por padrão: um deslocamento
  pretendido de (500,300) percorreu (934,560). O dispositivo virtual precisa de
  perfil *flat*, que o `install.sh` escreve no `kcminputrc`. Depois disso é 1:1.
- **O KWin soma movimentos relativos que chegam juntos e limita só o total.**
  Posicionar exige UM evento por transição: um salto mais uma correção não
  visita o canto, percorre a soma e prende o cursor na borda oposta.
- Ultrapassar o canto superior esquerdo para em **(1,1)**, não em (0,0).
- **`SetCursorPos` não funciona** no PC Windows testado. A colocação do cursor
  lá passa por `ClipCursor` num retângulo de 1x1, que funciona.
- **MTU do `tailscale0` é 1280**, e é por isso que o quadro de áudio tem 5 ms /
  960 bytes.

## Instalar no Linux

```bash
./infra/install.sh
```

Roda como usuário comum e pede sudo só nos passos que precisam. Ele compila,
instala em `/usr/local/bin`, cria o grupo que controla o socket, escreve o
perfil flat do dispositivo virtual e habilita as duas units.

## Instalar no Windows

Copie o `synk.exe` para o PC — é o único arquivo que precisa ir. Depois,
num PowerShell:

```powershell
.\infra\windows\install.ps1 -Hidden
```

Isso registra uma tarefa que inicia o link no seu logon e o reinicia se ele
morrer. `-Hidden` roda sem janela de console e manda a saída para um `.log` ao
lado do executável; sem ele, um console fica aberto — feio, mas é onde as
mensagens aparecem.

**No logon, não no boot:** este lado captura teclado e mouse com hooks de baixo
nível, que só existem dentro de uma sessão interativa. Uma tarefa "rodar mesmo
sem o usuário logado" não teria desktop para observar, e ainda pediria sua
senha guardada.

**Sem privilégio elevado**, porque capturar entrada comum não precisa. O efeito
colateral é conhecido: janelas rodando como administrador não entregam entrada
a um processo não elevado. Se isso incomodar, `-Elevated`.

Não é preciso esperar o Tailscale subir antes: o binário consulta o backend e o
estado do peer, recua de 1s a 30s e só escreve no log quando o motivo muda.

## O que ainda não existe

- **Direção do microfone** (Windows → fonte virtual do PipeWire).
- **WASAPI e a janela de ocultar ponteiro no Windows compilam e passam no vet,
  mas nunca foram executados.** Trate como não comprovado.
