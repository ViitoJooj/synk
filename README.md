# wfkit-link

Compartilha mouse, teclado, área de transferência e áudio entre uma máquina
Linux e um PC Windows na mesma tailnet. O ponteiro atravessa a borda da tela e
o teclado vai junto; não há agente na nuvem e não há servidor no meio.

Extraído de [wfkit](https://github.com/ViitoJooj/wfkit) em 2026-09-05, onde
vivia como subcomando do CLI. Virou projeto próprio porque é um app de duas
máquinas com ciclo de vida próprio, não parte do provisionamento de um desktop.

## Como está montado

```
cmd/link/        o binário: subcomandos session, daemon, status
internal/        transporte, protocolo, entrada, áudio, clipboard, foco
system/          units de systemd e install.sh
```

Dois processos no Linux, de propósito:

- **`wfkit-link daemon`** (root, unit de sistema) abre `/dev/uinput` e não faz
  mais nada. Sem rede — `PrivateNetwork=yes` —, sem áudio, sem clipboard. Ele
  injeta entrada e nunca lê. O único canal é um socket unix.
- **`wfkit-link session`** (seu usuário, unit de sessão) tem a conexão com o
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
./system/install.sh
```

Roda como usuário comum e pede sudo só nos passos que precisam. Ele compila,
instala em `/usr/local/bin`, cria o grupo que controla o socket, escreve o
perfil flat do dispositivo virtual e habilita as duas units.

## O que ainda não existe

- **Autostart no lado Windows.** O `.exe` precisa ser iniciado à mão. Enquanto
  não houver entrada no Agendador de Tarefas, a conexão não é automática — e
  isso vale tanto com o Linux escutando quanto discando, porque o processo
  precisa estar de pé nos dois casos.
- **Direção do microfone** (Windows → fonte virtual do PipeWire).
- **WASAPI e a janela de ocultar ponteiro no Windows compilam e passam no vet,
  mas nunca foram executados.** Trate como não comprovado.
