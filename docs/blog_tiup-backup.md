# TiUP 中控机高可用：用 tiup-backup 实现全自动备份与无缝接管

## 前言

用 TiUP 管理 TiDB 集群的同学，多半都想过这个问题：中控机要是坏了怎么办？
在 [AskTUG](https://pingkai.cn/tidbcommunity/forum/) 也出现过大量这类的讨论帖。

集群本身没毛病，TiDB、TiKV、PD 都在各自的机器上好好跑着。可是管理它们的 `~/.tiup` 目录只在中控机这一台机器上，集群拓扑、部署时生成的 SSH 密钥、下载好的组件，全都在里面。磁盘一坏或者机器一丢，这些东西就得靠手工一点点重建，想想就头大。

常见的做法是人工把 `~/.tiup` 打个包拷到另一台机器上，能用，但是得有人记得去做，而且每次扩缩容之后都要重来一遍。还有其他人工方式可参考 https://pingkai.cn/tidbcommunity/forum/t/topic/1062379/16?u=hey-hoho

其实各种方法还是不少的，但是我个人觉得不够自动化，操作方式不够`Tiup Native`。

所以我写了一个小工具 **tiup-backup**：不改 TiUP 的源码，用 `tiup backup ...` 的方式调用，把本机的 `~/.tiup` 同步到备份节点，**真出问题时让备份节点无需任何操作就可以接管 `tiup` 命令，体验丝滑到  `tiup` 没坏一样**。

这篇文章就来介绍一下它怎么用，以及可能踩到的几个坑。

> 项目地址：https://github.com/hey-hoho/tiups

## 先搞清楚中控机里存了什么

在开始之前，先把几个名词对齐一下：

- 中控机：执行 `tiup cluster ...` 的那台机器，也就是 `~/.tiup` 所在的地方。
- TIUP_HOME：TiUP 的家目录，默认是 `~/.tiup`，里面有 `bin`、`components`、`storage` 等。
- 集群元数据：`storage/cluster/clusters/<集群名>/meta.yaml`，记录集群的拓扑和版本。
- 集群密钥：同一个目录下 `ssh/` 里的 `id_rsa`，TiUP 部署集群时生成，对应的公钥已经写进了各集群节点的部署用户。
- 镜像：`tiup.toml` 里的 `mirror`，在线环境是一个 https 地址，离线环境是本地的一个目录。
- 备份节点：用来接管 `tiup` 的机器，需要和中控机同操作系统、同架构，不要求是 TiDB 集群所在的机器。

从这几项就能看出来，只要把整个 `~/.tiup` 完整地复制到另一台机器上，那台机器理论上就能接着管理这些集群。tiup-backup 做的就是这件事。


## 安装 tiup-backup

### 在线安装

和安装 tiup 本身一样，在已经装好 tiup 的机器上，一行命令就够了：

```
curl --proto '=https' --tlsv1.2 -sSf https://raw.githubusercontent.com/hey-hoho/tiups/main/scripts/install.sh | sh && . ~/.bashrc
```

```
$ curl --proto '=https' --tlsv1.2 -sSf https://raw.githubusercontent.com/hey-hoho/tiups/main/scripts/install.sh | sh && . ~/.bashrc
downloading https://github.com/hey-hoho/tiups/releases/latest/download/tiup-backup-linux-amd64.tar.gz
sha256 ok
installed /home/tidb/.tiup/bin/tiup-backup
added shell function to /home/tidb/.bashrc

Done. The tiup() function is only picked up by NEW shells. To use it in the
current shell, run this now (an installer cannot do it for you, it runs in
its own process):

  . /home/tidb/.bashrc

Then try:
  tiup backup list
  tiup backup add -N <ip,...> -u <user>
  tiup backup sync
Note: the function only exists in interactive shells; in scripts/cron call tiup-backup directly.
```
脚本会去项目的 GitHub Release 下载对应架构的发布包，同时下载它的 `.sha256` 做校验，校验不过就直接退出，什么都不会装。想指定版本的话，在后面加 `-s -- --version v0.1.0`：

```
curl --proto '=https' --tlsv1.2 -sSf https://raw.githubusercontent.com/hey-hoho/tiups/main/scripts/install.sh | sh -s -- --version v0.1.0 && . ~/.bashrc
```

### 离线安装

离线包下载地址：https://github.com/hey-hoho/tiups/releases/tag/v0.1.0

离线包是一个 tar.gz，里面有二进制和一个 `install.sh`，在已经装好 tiup 的机器上解压后执行：

```
$ tar -xvzf tiup-backup-linux-amd64.tar.gz 
tiup-backup
install.sh

$ ./install.sh && . ~/.bashrc
installed /home/tidb/.tiup/bin/tiup-backup
added shell function to /home/tidb/.bashrc

Done. Open a new shell (or run: . /home/tidb/.bashrc) and try:
  tiup backup list
  tiup backup add -N <ip,...> -u <user>
  tiup backup sync
Note: the function only exists in interactive shells; in scripts/cron call tiup-backup directly.
```

脚本会先确认二进制能在本机跑起来，再原子地替换到 `~/.tiup/bin`，然后往 `~/.bashrc` 里加一段带标记的 `tiup()` 函数，重复执行就是升级，不会重复添加。想卸载的话加 `--uninstall`，会把二进制、函数和定时任务一起清掉。

这里提醒一下：如果想在**当前这个终端**里马上用，后面的 `. ~/.bashrc` 就不能省。脚本是在子进程里跑的，改不了你当前终端的环境，装完直接敲 `tiup backup list`，走的还是真正的 tiup，就会报 `component backup not installed`。所以安装完会提示这一步，文档里也写成了一行命令。

当然也不是非做不可：函数已经写进了 `~/.bashrc`，新开的终端或者重新登录之后会自动生效；

### 安装结果验证

安装完可以验证一下命令是否可用：
```
$ tiup backup list
no backup nodes; use `tiup backup add -N <ip,...>`
```

## 第一次同步

### 添加备份节点

先告诉工具有哪些备份节点：

```
$ tiup backup add -N 10.0.0.2,10.0.0.3 -u tidb
added tidb@10.0.0.2:22
added tidb@10.0.0.3:22

$ tiup backup list
HOST                 PORT   USER         AUTH
10.0.0.2             22     tidb         default (agent/key/password)
10.0.0.3             22     tidb         default (agent/key/password)
```

备份节点没配 SSH 免密也没关系，同步的时候会提示输入一次密码，同一次同步里复用，密码不会写到磁盘上。如果已经手动配了免密，就直接用 ssh-agent 或者 `~/.ssh` 下的默认密钥，不用做任何额外设置。

第一次连某个节点时，会让你确认主机指纹，之后记录在 `$TIUP_HOME/storage/meta/known_hosts` 里。如果哪天指纹变了，工具会直接拒绝，加了 `--yes` 也绕不过去。

### 同步

接下来执行同步：

```
$ tiup backup sync
2026-11-24T08:04:02Z local /home/tidb/.tiup: 526 entries, 2 node(s)
The authenticity of host 10.0.0.2:22 can't be established.
SHA256:pnOA5sK77QM2YOneS8QqMPLRPX0XZhZi8+Er6CoK1Y4
Trust it and continue? [y/N] y
...
10.0.0.2           uploaded=455 (5621.3 MiB) unchanged=0 deleted=0 failed=0
                   PATH: added /home/tidb/.tiup/bin to PATH in /home/tidb/.bash_profile (takes effect in new login shells)
10.0.0.3           uploaded=455 (5621.3 MiB) unchanged=0 deleted=0 failed=0
                   PATH: added /home/tidb/.tiup/bin to PATH in /home/tidb/.bash_profile (takes effect in new login shells)
```

**小提示：** 如果 `tiup` 用到的组件非常多，第一次同步会比较慢（取决于节点之间的传输速度），请耐心等待。

同步完之后备份节点的 `tiup` 和你当前中控机一模一样。

这里会按文件大小和修改时间做增量，文件先写成临时文件再重命名，所以连正在运行的 `tiup` 二进制也可以放心替换。权限和符号链接都会保留，指向本机 `~/.tiup` 的绝对链接，会改写成节点上的路径。

再执行一次，看看增量的效果：

```
$ tiup backup sync
10.0.0.2           uploaded=0 (0.0 MiB) unchanged=455 deleted=0 failed=0
                   PATH: already configured in /home/tidb/.bash_profile
```

没有变化的文件一个都不会再传。

可以看到输出里还有一行 PATH，这是我专门加的：同步完之后，工具会学官方安装脚本的做法，按节点上 `$SHELL` 选好 profile，往里追加 `export PATH=...`。只会追加一段带标记的内容，不会改你已有的行，所以在备份节点上重新登录，就能直接敲 `tiup` 了。

### 移除备份节点

节点下线或者不想再备份了，用 `remove` 把它从列表里去掉就行：

```
$ tiup backup remove -N 10.0.0.3
removed 10.0.0.3
```

要注意的是，`remove` 只改备份节点列表，之后的 `sync` 不会再往这台机器上传东西，但是不会去动它上面已经有的内容。之前同步过去的 `~/.tiup`、`.bash_profile` 里追加的 PATH 都还在，不需要的话得自己登录上去清理。如果写错了节点地址，它只会给一个 `is not a backup node` 的警告，不会影响其他节点。

## 让它自己定时跑

到这里貌似想要的功能都做到了，但每次 `tiup` 变更完还要做 `tiup backup sync` 不免还是有点繁琐。

手工同步总有忘记的时候，所以我又加了个 `tiup backup job`：

```
$ tiup backup job -s "0 2 * * *"
  10.0.0.2           key auth ok
  10.0.0.3           key auth ok
scheduled sync set: 0 2 * * *
log: /home/tidb/.tiup/storage/meta/sync.log
```

它做的事情很朴素，就是在当前用户的 crontab 里加一行带标记的任务，定时执行 `tiup-backup sync`。只会动带标记的那一行，你自己写的其他任务不受影响，加 `--disable` 就能删掉。

不过有个前提要注意：**cron 没法输入密码**。所以设置任务的时候，工具会用纯密钥方式把每个节点都连一遍，有一个连不上就直接拒绝，不会给你留下一个半夜失败的定时任务。解决办法就是提前配好免密，或者在 `tiup backup add` 的时候用 `-i` 指定私钥。另外建议先手动同步一次，把各节点的主机指纹都确认过。

输出会写到 `sync.log`，直接敲 `tiup backup job` 就能看到当前的计划和最后几行日志。手动同步和定时同步同时触发的话，后面的会被一个文件锁挡掉，不用担心互相踩踏。

## 备份之后要注意的几件事

备份 tiup 和真正能管理集群，中间还隔着几件事，下面这几点可能是比较容易遇到的问题。

### SSH 互信

TiUP 中控机要想操作集群，必须能 SSH 免密到每一个集群节点，这一点相信大家都知道。

如果你是用 tiup 自己生成的集群互信密钥，它们会保存在 `~/.tiup` 里，已经跟着一起同步过去了。同步到备份节点之后，`tiup cluster exec` 直接就能用同步过来的密钥登录集群节点，把密钥文件挪走的话，立刻报 `unable to authenticate`，说明起作用的确实是它。推荐用这种方式。

但是有两种情况不在这个范围里：一是部署时用 `-i` 指定了 `~/.tiup` 之外的私钥，二是密钥只存在于 ssh-agent 里，这两种需要你自己在备份节点上做好互信。网络上也别忘了，备份节点到集群节点的 SSH 端口得是通的。总之，真正动手之前，先在备份节点上执行一次 `tiup cluster display <集群名>`，确认连得通再说。

### 离线镜像

很多时候我们部署 TiDB 集群都是用的离线镜像。同步完之后，在备份节点上敲 `tiup`，会报这么一个错：

```
Error: stat /home/tidb/tidb-community-server-v8.5.4-linux-amd64: no such file or directory
```

原因也很好理解：中控机是用离线包装的，`tiup.toml` 里的镜像是中控机本地的一个目录。`sync` 把 `tiup.toml` 原样复制了过去，但这个目录本身在 `~/.tiup` 之外，没有被复制，备份节点上自然就找不到了。

现在 `sync` 发现这种情况会主动给出 WARNING。解决办法有两个。

办法一，加 `--with-mirror`，把镜像目录按相同的路径一并同步过去：

```
$ tiup backup sync --with-mirror
2026-11-24T17:51:03+08:00 local /home/tidb/.tiup: 526 entries, 1 node(s)
local mirror /home/tidb/tidb-community-server-v8.5.4-linux-amd64: 188 entries
10.0.0.2           uploaded=1 (5.6 MiB) unchanged=525 deleted=0 failed=0
                   mirror /home/tidb/tidb-community-server-v8.5.4-linux-amd64: uploaded=171 (3215.6 MiB) unchanged=0 deleted=0 failed=0
                   PATH: already configured in /home/tidb/.bash_profile
```

离线包有好几个 GB，第一次会比较久，之后都是增量。手动跑一次就可以了，之后定时任务里不带参数的 `sync` 就够用。

办法二，让备份节点用自己的镜像，在节点上手动操作：

```
$ tar xzf tidb-community-server-v8.5.4-linux-amd64.tar.gz -C /data
$ tiup mirror set /data/tidb-community-server-v8.5.4-linux-amd64
$ tiup mirror show
```

需要留意的是，每次 `sync` 都会把整个 `~/.tiup` 复制过去，包括 `tiup.toml`，节点上自己设的镜像会被下一次同步覆盖回去。所以办法二更适合那种已经要接管的节点，或者你愿意每次同步后重新执行一次 `tiup mirror set`。如果开了定时同步，我更推荐办法一。

### 安全风险

`~/.tiup` 里有集群的 SSH 私钥，所以备份节点请用你信得过的账号，别随便找台机器就同步过去。`--delete` 默认是关着的，需要的时候才会删除节点上多出来的文件，想先看看会发生什么可以用 `--dry-run`。

还有一点必须说清楚：备份是定时的，不是实时的。两次同步之间如果你做了扩缩容，备份节点上的元数据就是旧的，所以改动集群之后，顺手再 `tiup backup sync` 一次是个好习惯。

更多用法大家可以去探索  `tiup backup --help`。

## 未来展望

目前的 tiup-backup 解决的是“有备份、能接管、自动化”，接管本身还是要人来操作，并没有做自动的故障切换。我觉得这个定位对运维工具来说反而比较踏实，毕竟中控机到底算不算挂了，该不该切，还是人来判断更放心。

反正用下来的感受就是：中控机上那一堆不起眼的文件，终于有了第二份，心里踏实了不少。如果你也有类似的需求，欢迎试用，有问题和建议也欢迎留言交流。
