# tiup-backup

[English](README.md) | 简体中文

把本机的 `~/.tiup`（或 `$TIUP_HOME`）复制到备份节点，让备份节点在需要时可以直接接管 `tiup`。

```
tiup backup add -N 10.0.0.2,10.0.0.3 [-u tidb] [-P 22] [-i ~/.ssh/id_rsa] [--remote-dir DIR]
tiup backup remove -N 10.0.0.3
tiup backup list
tiup backup sync [-N 10.0.0.2] [--delete] [--dry-run] [--with-mirror] [--no-path] [--yes]
tiup backup job [-s "0 2 * * *"] [--disable]
tiup backup init-shell
```

| 命令 | 作用 |
|---|---|
| `add` / `remove` | 添加、删除备份节点（逗号分隔多个 IP） |
| `list` | 列出所有备份节点及其认证方式 |
| `sync` | 把本机的 `~/.tiup` 同步到所有（或 `-N` 指定的）备份节点 |
| `job` | 查看或设置定时同步任务（写入当前用户的 crontab） |
| `init-shell` | 输出 shell 函数，使 `tiup backup ...` 路由到本工具 |


> `sync` 会复制 tiup 在 `storage/cluster/clusters/<集群名>/ssh/` 下为集群生成的密钥，但不包括你用 `-i`
> 指定的、位于 `~/.tiup` 之外的密钥，也不包括只存在于 ssh-agent 里的密钥。

## 快速开始

第 1~4 步在当前运行 tiup 的机器上执行，第 5 步在备份节点上执行。

```
# 1. 安装（会在 ~/.bashrc 里加入 `tiup backup ...` 函数，然后加载）
curl --proto '=https' --tlsv1.2 -sSf https://raw.githubusercontent.com/hey-hoho/tiups/main/scripts/install.sh | sh
. ~/.bashrc

# 2. 添加备份节点（操作系统、架构要与本机相同）
tiup backup add -N 10.0.0.2,10.0.0.3 -u tidb

# 3. 先预览，再同步整个 ~/.tiup（第一次会让你确认信任每个节点的主机指纹）
tiup backup sync --dry-run
tiup backup sync

# 4. 可选：每天凌晨 2 点自动同步（需要到各节点的密钥免密登录）
tiup backup job -s "0 2 * * *"

# 5. 在备份节点上，用新的登录 shell 接管
ssh tidb@10.0.0.2
tiup cluster list
```

如果本机用的是离线镜像（`tiup mirror set /path/...`），第 3 步要加 `--with-mirror`，否则节点上会报
`stat ...: no such file or directory`，见[离线（本地）镜像](#离线本地镜像)。每一步的细节见后面的各节。

## 离线（本地）镜像

如果源机器是用离线包装的（`tiup mirror set /path/to/tidb-community-server-...`），`tiup.toml` 里记录的是这个
本地目录，它在 `~/.tiup` 之外。`sync` 会复制 `tiup.toml`，但不会复制这个目录，于是在备份节点上执行任何 tiup
命令都会报：

```
Error: stat /path/to/tidb-community-server-...: no such file or directory
```

`sync` 检测到这种情况会打印 WARNING。有两种解决办法：

**办法一：`--with-mirror`（把镜像按相同路径复制到每个节点）**

```
tiup backup sync --with-mirror
```

镜像目录会被复制到每个节点的相同绝对路径下（离线包有好几个 GB，第一次会比较久，之后是增量）。
手动执行一次即可，之后定时任务里不带参数的 `sync` 就够了。节点用户需要有权限创建这个路径。
如果同时加了 `--delete`，节点上这份镜像里多余的文件也会被删除。

**办法二：节点使用自己的镜像（在节点上手动操作）**

每次 `sync` 都会复制整个 `~/.tiup`（包括 `tiup.toml`），所以节点自己设置的镜像会被下一次同步覆盖。
这个办法适合已经要接管的节点，或者每次同步后重新执行第 3 步。

1. 把离线包上传到节点并解压，例如：
   `tar xzf tidb-community-server-v8.5.4-linux-amd64.tar.gz -C /data`
2. 可选：离线包自带 `local_install.sh`，它会从包里安装 tiup 并设置镜像；这里 tiup 已经有了，可以跳过。
3. 让 tiup 指向节点上的这份镜像：`tiup mirror set /data/tidb-community-server-v8.5.4-linux-amd64`
4. 用 `tiup mirror show` 和 `tiup list` 检查，都不应再出现 `stat ...: no such file or directory`。

## 同步机制

- **认证**：指定 `-i` 时只用该密钥；否则依次尝试 ssh-agent、`~/.ssh/id_ed25519|id_ecdsa|id_rsa`，
  最后是密码（只提示一次并复用，或从环境变量 `TIUP_META_PASSWORD` 读取）。密码不会被保存。
  不支持带口令的私钥文件（请使用 ssh-agent）。
- **主机指纹**：首次连接信任（TOFU），记录在 `$TIUP_HOME/storage/meta/known_hosts`（该文件不会被同步）。
  指纹发生变化时一律拒绝，`--yes` 也不能绕过。
  非交互环境下遇到未知主机会拒绝，需要加 `--yes` 才会信任。
- **传输**：只用 SFTP，节点上无需安装 rsync。按文件大小 + 修改时间做增量，临时文件加原子重命名，
  保留权限和符号链接（指向 `$TIUP_HOME` 的绝对符号链接会改写成节点上的路径）。
- **PATH**：与官方安装脚本类似，`sync` 会在节点用户的 profile 中追加
  `export PATH=<目录>/bin:$PATH`（使用自定义 `--remote-dir` 时还会加上 `TIUP_HOME`）。
  profile 按节点的 `$SHELL` 选择：依次取 `~/.<shell>_profile`、`_login`、`rc` 中第一个存在的，否则用 `~/.profile`。
  只追加带标记的块，不会改写已有内容；如果 `.tiup/bin` 已经在里面则什么都不做。
  只对新的登录 shell 生效（`ssh` 登录、`bash -l`），`ssh node tiup ...` 这类非登录命令不会读取它。
  `--no-path` 可跳过此步骤；fish/csh 会被跳过并给出提示。
- 会复制完整的 `~/.tiup`，不支持排除其中的部分内容。只有本工具自身的机器状态不会同步：
  `storage/meta/known_hosts`、`sync.log`、`sync.lock`。
- `--delete` 需要显式开启，会删除节点上本机 `~/.tiup` 里没有的文件。
- 备份节点必须与本机操作系统、架构相同（tiup 二进制是原样复制的）。
- 节点列表保存在 `$TIUP_HOME/storage/meta/backup_nodes.json`，同样会被同步。
- `~/.tiup` 中包含集群的 SSH 私钥，请只把它同步到可信账号。

## 定时同步（`tiup backup job`）

- `tiup backup job` 显示当前的定时任务和日志末尾几行；`-s CRON` 在当前用户的 crontab 里创建或替换任务
  （5 段表达式或 `@daily` 等，会校验每段的取值范围）；`--disable` 删除任务。
  只会操作带 `# tiup-backup:sync` 标记的那一行，不动其他任务。
- cron 无法输入密码，所以每个节点都必须支持密钥认证：自己配好 SSH 免密（agent 或默认密钥，不需要 `-i`），
  或者 `tiup backup add -N ... -i KEY`。`job -s` 会用纯密钥方式连接每个节点，任何一个失败就拒绝设置。
  请先手动执行一次 `tiup backup sync`，让各节点的主机指纹被信任。
- 输出写入 `$TIUP_HOME/storage/meta/sync.log`（不同步，不轮转）。文件锁保证手动和 cron 触发的同步不会同时运行。
- 需要系统有 `crontab` 命令和正在运行的 cron 服务（例如 `dnf install cronie`）。

## 构建与打包

```
make            # vet + test + 构建到 dist/
make package    # dist/tiup-backup-linux-{amd64,arm64}.tar.gz（附带 .sha256），每个包含二进制和 install.sh
```

没有 make 时：`go build -o dist/tiup-backup ./cmd/tiup-backup`
（交叉编译用 `GOOS=linux GOARCH=amd64 CGO_ENABLED=0`）。

发布版本的步骤：执行 `make package`，在 <https://github.com/hey-hoho/tiups> 新建一个 GitHub Release，把
`tiup-backup-linux-{amd64,arm64}.tar.gz` 和 `tiup-backup-linux-{amd64,arm64}.tar.gz.sha256` 这四个文件作为附件
上传（一行命令安装需要同时有包和对应的 `.sha256`）。`install.sh` 本身直接从仓库源码获取，不用上传。
想发布到别处的话，用 `make package BASE_URL=https://example.com/dir`，会把这个下载地址写进 `dist/install.sh`。

## 安装（`scripts/install.sh`）

### 一行命令

在已经装有 tiup 的机器上（存在 `$TIUP_HOME/bin`），写法和安装 tiup 本身一样：

```
curl --proto '=https' --tlsv1.2 -sSf https://raw.githubusercontent.com/hey-hoho/tiups/main/scripts/install.sh | sh
```

需要传参数时放在 `sh -s --` 后面，比如指定版本而不是最新版，或者不修改 shell 配置：

```
curl --proto '=https' --tlsv1.2 -sSf https://raw.githubusercontent.com/hey-hoho/tiups/main/scripts/install.sh | sh -s -- --version v0.1.0
curl --proto '=https' --tlsv1.2 -sSf https://raw.githubusercontent.com/hey-hoho/tiups/main/scripts/install.sh | sh -s -- --no-shell
```

脚本会从本项目的 GitHub Release 下载 `tiup-backup-<os>-<arch>.tar.gz` 和对应的 `.sha256`
（`--version TAG` 或环境变量 `TIUP_BACKUP_VERSION` 指定版本，`--base-url URL` 或 `TIUP_BACKUP_BASE_URL`
指向其他地址），校验通过才会安装；校验不一致、缺少校验文件、或者地址不是 https 时都会中止，什么都不装
（`--no-verify` 可跳过校验），跳转到 http 的重定向也会被拒绝。因为 GitHub 在有些网络下不稳定，下载失败
（连接被拒绝或重置、5xx、传输卡住）会自动重试，最多 8 次，每次间隔 2 秒（用 `TIUP_BACKUP_RETRIES=N`
修改次数）；404 这类明确的答复不会重试。整个脚本包在一个函数里，最后一行才调用，所以下载中途被截断时
不会执行任何内容。目前只发布 linux 的 amd64 和 arm64 包。

有些网络会挡住 `raw.githubusercontent.com`（TLS 连接被重置），上面这条命令在脚本开始执行之前就会失败。
遇到这种情况，请换别的方式下载 `install.sh`，或者解压发布包后运行包里的 `install.sh`（见下一节）。

### 解压发布包后安装

```
./install.sh && . ~/.bashrc             # 或：./install.sh -f tiup-backup-linux-amd64.tar.gz
./install.sh --tiup-home /data/tiup     # 非默认的 TiUP 目录
./install.sh --no-shell                 # 只装二进制，不修改 ~/.bashrc / ~/.zshrc
./install.sh --uninstall                # 删除二进制、shell 函数和定时任务
```

安装脚本在自己的子进程里运行，改不了你当前的 shell，所以需要自己执行 `. ~/.bashrc`（zsh 用 `. ~/.zshrc`）
来加载新函数，上面用 `&&` 就是一行搞定；否则重新打开一个 shell 即可。

脚本会先确认二进制能在本机运行，再原子地复制到 `$TIUP_HOME/bin/tiup-backup`（构建有问题时不会覆盖可用的旧版本），
并在 `~/.bashrc` / `~/.zshrc` 中加入带标记的 `tiup()` 函数块。重复执行即升级二进制，shell 块保持不变。
`--uninstall` 会还原 rc 文件，并保留 `$TIUP_HOME/storage/meta` 中的节点列表。

`tiup backup sync` 已经会把 `bin/tiup-backup` 复制到备份节点，但节点上的 shell 函数还需要执行一次
`install.sh`（或 `tiup-backup init-shell >> ~/.bashrc`）才能用 `tiup backup ...` 这种写法。

## 为什么用 shell 函数（tiup 拒绝运行未签名的本地组件）

tiup 只运行当前镜像签名索引（manifest）里列出的组件，所以把二进制放进 `~/.tiup/components/` 是不够的。
shell 函数把 `tiup backup ...` 转给 `tiup-backup`，其他命令仍交给真正的 tiup。
它只存在于交互式 shell 中；脚本和 cron 必须直接调用 `tiup-backup`。
另一种做法是把组件发布到私有镜像：`tiup mirror publish backup v0.1.0 <tar.gz> tiup-backup`。

## 项目结构

```
cmd/tiup-backup/     程序入口
internal/cli/        cobra 命令：add、remove、list、sync、job、init-shell
internal/config/     TIUP_HOME 解析、备份节点列表、状态文件路径
internal/remote/     SSH + SFTP 连接：认证方式、主机指纹校验
internal/syncer/     本地扫描、SFTP 传输、节点 PATH 配置
internal/cron/       定时同步的 crontab 条目
internal/lock/       sync 的单实例文件锁
internal/shell/      shell 引用函数和 init-shell 片段
scripts/install.sh   一键安装到 tiup 环境的脚本
Makefile             构建、测试和发布打包
```
