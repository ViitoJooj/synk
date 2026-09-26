# synk

Compartilha mouse, teclado, área de transferência e áudio entre uma máquina
Linux e um PC Windows na mesma tailnet, e mantém pastas iguais nas duas. O
ponteiro atravessa a borda da tela e o teclado vai junto; não há agente na
nuvem e não há servidor no meio.

Extraído de [wfkit](https://github.com/ViitoJooj/wfkit) em 2026-09-05, onde
vivia como subcomando do CLI. Virou projeto próprio porque é um app de duas
máquinas com ciclo de vida próprio, não parte do provisionamento de um desktop.
Chamou-se `wfkit-link` até 2026-09-24 e `conn` até 2026-09-25.
`hacks/install.sh` e `infra/windows/install.ps1` migram uma instalação com
qualquer um dos dois nomes.

## Como está montado

```
cmd/synk/          o binário: só chama internal/cli
internal/cli/      os comandos, um arquivo por comando
internal/agent/    o processo que fica de pé: link, clipboard, áudio
internal/injector/ o daemon root que abre /dev/uinput
internal/service/  liga, desliga e lê o estado das units e da tarefa agendada
internal/          transporte, protocolo, entrada, áudio, clipboard, foco
internal/control/  a API local: socket unix / named pipe, JSON por linha
internal/sync/     a sincronização de pastas: index (varredura), plan (as
                   decisões, função pura), trash, folders (lista e bloqueios),
                   engine (sessão, protocolo, aplicação)
pkg/errorx/        os erros com código e dica (snk-NN), no formato do devxx
infra/linux/       as units de systemd
infra/windows/     install.ps1
hacks/install.sh   instala no Linux e migra os nomes antigos
```

## Comandos

```
synk <peer>        passa a ligar com essa máquina e reinicia o agente. Aceita o
                   nome ou o SO: "synk windows" escolhe o único Windows online
synk ls            as máquinas da tailnet; * marca a atual
synk status [-w]   agente, link, peer e ajustes numa tela; -w segue ao vivo
synk doctor        confere tudo de que o link depende e diz como consertar
synk up | down | restart
synk logs [-f] [-n 50] [--injector]
synk config ls | get <chave> | set <chave> <valor>

synk folder add <caminho> [--as nome] | ls | show <nome> | rm <nome>
synk folder accept <nome> [--keep-here repo] [--take-peer repo]
synk folder pause | resume <nome> [--accept-deletes]
synk block <caminho|padrão> | block ls | unblock <caminho|padrão>
synk trash ls [pasta] | trash restore <id> [--force]
```

Flags globais: `--json` (para scripts), `-q`, `--peer`, `--port`. Os erros
saem com código, como no devxx:

```
code: snk-01
error: more than one macos machine is online (mac, mac-old)
error_type: user error.
message: name the one you want: synk mac
```

## API de controle

O agente responde numa API local, que é por onde `synk status`, `synk <peer>`,
`synk config set` e a UI falam com ele. É JSON, uma mensagem por linha, num
socket unix no Linux (`$XDG_RUNTIME_DIR/synk/control.sock`, 0600, e o uid de
quem conecta é conferido) e num named pipe no Windows
(`\\.\pipe\synk-control-<usuário>`, com ACL só do usuário). Uma conexão
leva um pedido e a resposta:

```
→ {"verb":"connect","args":{"peer":"windows"}}
← {"ok":true,"result":{"name":"desktop-6v4goh8","os":"windows",...}}
```

Verbos: `status`, `peers`, `connect`, `reload`, `config.ls`, `config.set` e
`events`, que fica aberto e manda o status inteiro a cada mudança. Os erros
vêm com o mesmo código e dica da linha de comando.

`connect`, `reload` e `config.set` reiniciam só o link, dentro do processo: o
socket continua de pé, e no Windows não se espera o minuto da tarefa
agendada. Uma config que não carrega deixa o link antigo rodando.

É separada do socket do injetor de propósito: por ali se pede para digitar,
por aqui só se muda ajuste.

`synk agent` e `synk injector` são o que o systemd e a tarefa agendada rodam,
e ficam fora da ajuda. Os nomes antigos, `session` e `daemon`, continuam
aceitos para as units já instaladas.

A direção não muda com `synk <peer>`: o Linux continua escutando e o Windows
continua discando. No Linux o peer é gravado no `config.yaml`, trocando só a
linha `peer:`. No Windows, que não lê config de propósito, fica num arquivo
`peer` com um nome só, em `%APPDATA%\synk\`.

## Configuração

Só o Linux tem config: `~/.config/synk/config.yaml`. É ele que recebe a
entrada, então é ele que sabe onde fica a tela do outro. O Windows descobre o
resto no hello da conexão.

```yaml
peer: desktop-6v4goh8   # a máquina do outro lado, como o `synk ls` mostra
edge: right             # a borda desta tela além da qual fica a do peer
clipboard: true
audio:
  playback: true        # o som daqui sai nas caixas do peer
  mic: true
# port: 27431           # o padrão; 24800 é do Synergy/Deskflow, evite
```

`synk config set audio.mic off` troca uma linha e deixa o resto do arquivo,
comentários inclusive, como estava. Com o agente rodando, ele passa a usar o
valor na hora.

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
./hacks/install.sh
```

Roda como usuário comum e pede sudo só nos passos que precisam. Ele compila,
instala em `/usr/local/bin/synk`, cria o grupo `synk` que controla o socket,
escreve o perfil flat do dispositivo virtual e habilita as duas units:
`synk-injector` (sistema) e `synk-agent` (usuário). Rodar de novo não estraga
nada.

Se achar uma instalação de `wfkit-link` ou `conn`, ele migra: copia antes
tudo o que vai apagar ou reescrever para `~/.local/share/synk/backup-<data>/`,
leva a config para `~/.config/synk/config.yaml`, **renomeia** o grupo antigo
para `synk` (o GID continua o mesmo, então não precisa deslogar), troca o
perfil do ponteiro para o nome novo do dispositivo e só então desliga o velho e
liga o novo. O link fica fora do ar só por esses segundos. O protocolo não
mudou: um PC ainda rodando o exe antigo continua conectando.

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

## Sincronização de pastas

Mantém pastas iguais nas duas máquinas: salvou no PC, em alguns segundos está
no notebook, e o contrário. Roda dentro do agente, numa conexão própria na
porta seguinte à do link (27432), na mesma direção dele (o Linux escuta, o
Windows disca) e autenticada do mesmo jeito, pelo IP da tailnet.

**Uma pasta é pareada pelo nome**, não pelo caminho:
`synk folder add ~/Documentos/codes --as codes` num lado e o mesmo nome no
outro, onde quer que cada um guarde. A lista fica em `folders.yaml`, ao lado da
config, nas duas máquinas.

### O que nunca acontece

- **Nada é apagado de verdade.** O que a sync apaga ou sobrescreve vai antes
  para a lixeira (`~/.local/share/synk/trash`, `%LOCALAPPDATA%\synk\trash`),
  fora de toda pasta sincronizada, por 30 dias. `synk trash restore` devolve.
  Substituir um arquivo nunca deixa o caminho vazio: a versão velha é ligada
  na lixeira e a nova renomeada por cima.
- **A primeira sincronização não escreve nada antes de você aceitar**, em cada
  lado. `synk folder show` mostra o que vem, o que vai e o que existe nos dois
  com conteúdo diferente. Aceita, ela não apaga nada e não sobrescreve nada: o
  que difere fica nas duas versões, a mais recente com o nome original e a
  outra como `README (samsung-book).md`.
- **Uma deleção só passa se o arquivo apagado era a versão que os dois lados
  tinham combinado** (o hash do estado acordado). Se o outro lado editou nesse
  meio-tempo, a edição vence e o arquivo volta.
- **Trava de deleção em massa:** mais de 50 arquivos, ou mais de 25% da pasta,
  somados no último minuto, e a pasta pausa esperando
  `synk folder resume --accept-deletes`. Somado por minuto porque um `rm -rf`
  pego pela metade chega em duas rodadas, e cada metade sozinha passaria.
- **Pasta que sumiu** (disco desmontado, renomeada) pausa. Ela nunca é criada
  vazia no lugar, que é como o synk antigo transformava um disco desmontado
  em "apagar tudo" do outro lado.
- **Arquivo que não dá para ler** (travado por um programa no Windows) fica
  retido, nunca some do índice: sumir seria lido como apagado.
- **O peer só lê o que este lado indexou**, com o hash que o índice registrou.
  Pedido de caminho fora da pasta, bloqueado ou de outra versão é recusado sem
  mandar um byte. Caminhos que chegam são conferidos (`..`, absoluto, nomes
  que o Windows não aceita) e nenhuma escrita atravessa link simbólico.

### Conflitos, git e o `+x`

- **Os dois editaram o mesmo arquivo** antes de sincronizar: fica a versão mais
  recente; a outra vai para a lixeira de quem a tinha.
- **Repositórios git vão inteiros, `.git` junto**, para commits e branches
  chegarem ao outro lado. Enquanto o git trabalha num repo (existe um `*.lock`
  no `.git`), aquele repo inteiro fica retido nos dois lados e não sai pela
  metade.
- **Um repo que existe nos dois lados com conteúdo diferente** fica retido na
  primeira sincronização, porque juntá-lo arquivo por arquivo o quebraria.
  Escolha um lado no aceite, `--keep-here` numa máquina e `--take-peer` na
  outra: `synk folder accept codes --take-peer proj`. Os arquivos do lado que
  perde vão para a lixeira dele. Faça `push` no lado que perde antes, para não
  deixar commit para trás. Só `push/pull` nunca destrava: os arquivos do
  próprio git (index, logs) diferem entre dois clones mesmo com os mesmos
  commits.
- **Use uma máquina por vez em cada repo.** Rodar git no mesmo repo nos dois
  lados dentro dos segundos da sincronização é o único jeito de dar problema.
- No Windows, `core.autocrlf=true` reescreve arquivos a cada checkout e vira
  uma enxurrada de "edições"; o `synk doctor` avisa.
- O Windows não tem bit de executável. Um script editado lá volta ao Linux com
  o `+x` que tinha.

### Bloqueios

`synk block` tira algo da sincronização **nos dois sentidos**: nada ali sai
daqui e nada que chega é escrito ali. Um padrão sem barra casa com um nome em
qualquer lugar (`node_modules`, `*.env`); um caminho começa com `.`, `~` ou `/`.
Bloquear não apaga nada. Vêm bloqueados de fábrica, e podem sair com
`synk unblock`, os diretórios de build (`node_modules`, `target`, `bin`, `obj`,
`.venv`, `venv`, `__pycache__`, `dist`). Não saem nunca: o lixo dos sistemas
(`Thumbs.db`, `desktop.ini`, `~$*`...) e os caminhos que nenhuma pasta pode
ser nem conter: `~/.ssh`, `~/.gnupg`, chaveiros, perfis de navegador, a config
e o estado do synk, a home inteira, `/`, `C:\Windows`, `Program Files`.

### Como decide

Cada lado varre suas pastas a cada 5 s (o hash só é refeito quando tamanho ou
data mudam), troca o índice com o outro e roda a mesma função pura
(`internal/sync/plan`) sobre três visões: a sua, a do outro e a última em que
os dois concordaram. Cada lado só muda a própria cópia, e os dois convergem sem
coordenar mais nada. O estado acordado fica em `~/.local/state/synk`; se ele se
corromper, a próxima rodada é tratada como primeira sincronização, que não
apaga nada.

## O que ainda não existe

- **Direção do microfone** (Windows → fonte virtual do PipeWire).
- **WASAPI e a janela de ocultar ponteiro no Windows compilam e passam no vet,
  mas nunca foram executados.** Trate como não comprovado.
