# trzsz-go ( trz / tsz ) - 推荐 Go 版 trzsz

[![MIT License](https://img.shields.io/badge/license-MIT-green.svg?style=flat)](https://choosealicense.com/licenses/mit/)
[![GitHub Release](https://img.shields.io/github/v/release/trzsz/trzsz-go)](https://github.com/trzsz/trzsz-go/releases)
[![WebSite](https://img.shields.io/badge/WebSite-https%3A%2F%2Ftrzsz.github.io%2Fgo-blue?style=flat)](https://trzsz.github.io/go)
[![中文文档](https://img.shields.io/badge/%E4%B8%AD%E6%96%87%E6%96%87%E6%A1%A3-https%3A%2F%2Ftrzsz.github.io%2Fcn%2Fgo-blue?style=flat)](https://trzsz.github.io/cn/go)

`trzsz-go` 使所有支持本地 shell 的终端都支持 [trzsz](https://trzsz.github.io/cn/) ( trz / tsz —— 和 rz / sz 类似、兼容 tmux )。

⭐ 推荐在服务器上使用 go 版 `trzsz`，在本地电脑使用 [trzsz-ssh ( tssh )](https://trzsz.github.io/cn/ssh)。

_有关 `trzsz ( trz / tsz )` 更详细的文档，请查看 [https://trzsz.github.io/cn](https://trzsz.github.io/cn/)。_

## 安装方法

- Ubuntu 可用 apt 安装

  <details><summary><code>sudo apt install trzsz</code></summary>

  ```sh
  sudo apt update && sudo apt install software-properties-common
  sudo add-apt-repository ppa:trzsz/ppa && sudo apt update

  sudo apt install trzsz
  ```

  </details>

- Debian 可用 apt 安装

  <details><summary><code>sudo apt install trzsz</code></summary>

  ```sh
  sudo apt install curl gpg
  curl -s 'https://keyserver.ubuntu.com/pks/lookup?op=get&search=0x7074ce75da7cc691c1ae1a7c7e51d1ad956055ca' \
      | gpg --dearmor -o /usr/share/keyrings/trzsz.gpg
  echo 'deb [signed-by=/usr/share/keyrings/trzsz.gpg] https://ppa.launchpadcontent.net/trzsz/ppa/ubuntu jammy main' \
      | sudo tee /etc/apt/sources.list.d/trzsz.list
  sudo apt update

  sudo apt install trzsz
  ```

  </details>

- Linux 可用 yum 安装

  <details><summary><code>sudo yum install trzsz</code></summary>

  - 国内推荐使用 [wlnmp](https://www.wlnmp.com/install) 源，安装 trzsz 只需要添加 wlnmp 源（ 配置 epel 源不是必须的 ）：

    ```sh
    curl -fsSL "https://sh.wlnmp.com/wlnmp.sh" | bash

    sudo yum install trzsz
    ```

  - 也可使用 [gemfury](https://gemfury.com/) 源（ 只要网络通，所有操作系统通用 ）

    ```sh
    echo '[trzsz]
    name=Trzsz Repo
    baseurl=https://yum.fury.io/trzsz/
    enabled=1
    gpgcheck=0' | sudo tee /etc/yum.repos.d/trzsz.repo

    sudo yum install trzsz
    ```

  </details>

- ArchLinux 可用 [yay](https://github.com/Jguer/yay) 安装

  <details><summary><code>yay -S trzsz</code></summary>

  ```sh
  yay -Syu
  yay -S trzsz
  ```

  </details>

- MacOS 可用 [homebrew](https://brew.sh/) 安装

  <details><summary><code>brew install trzsz-go</code></summary>

  ```sh
  brew update
  brew install trzsz-go
  ```

  </details>

- Windows 可用 [scoop](https://scoop.sh/) / [winget](https://learn.microsoft.com/zh-cn/windows/package-manager/winget/) / [choco](https://community.chocolatey.org/) 安装

  <details><summary><code>scoop install trzsz</code> / <code>winget install trzsz</code> / <code>choco install trzsz</code></summary>

  ```sh
  scoop install trzsz
  ```

  ```sh
  winget install trzsz
  ```

  ```sh
  choco install trzsz
  ```

  </details>

- 用 Go 直接安装（ 要求 go 1.20 以上 ）

  <details><summary><code>go install github.com/trzsz/trzsz-go/cmd/...@latest</code></summary>

  ```sh
  go install github.com/trzsz/trzsz-go/cmd/trz@latest
  go install github.com/trzsz/trzsz-go/cmd/tsz@latest
  go install github.com/trzsz/trzsz-go/cmd/trzsz@latest
  ```

  安装后，`trzsz` 程序一般位于 `~/go/bin/` 目录下（ Windows 一般在 `C:\Users\your_name\go\bin\` ）。

  </details>

- 用 Go 自己编译（ 要求 go 1.20 以上 ）

  <details><summary><code>sudo make install</code></summary>

  ```sh
  git clone --depth 1 https://github.com/trzsz/trzsz-go.git
  cd trzsz-go
  make
  sudo make install
  ```

  </details>

- 可从 [GitHub Releases](https://github.com/trzsz/trzsz-go/releases) 中下载，国内可从 [Gitee 发行版](https://gitee.com/trzsz/trzsz-go/releases) 中下载，解压并加到 `PATH` 环境变量中。

## 使用方法

### 在本地电脑使用

- 在命令前添加 `trzsz`，就可以支持 trzsz ( trz / tsz ) 上传和下载了，如：

  ```sh
  trzsz bash
  trzsz PowerShell
  trzsz ssh x.x.x.x
  ```

- 在命令前添加 `trzsz --dragfile`，就可以启用拖拽上传功能，如：

  ```sh
  trzsz -d ssh x.x.x.x
  trzsz --dragfile ssh x.x.x.x
  ```

### 在跳板机上使用

- 如果在跳板机上使用 `tmux`，则需要再使用 `trzsz --relay ssh` 登录远程服务器，如：

  ```sh
  trzsz ssh jump_server
  tmux
  trzsz --relay ssh remote_server
  ```

### 在服务器上使用

- 与 lrzsz ( rz / sz ) 类似，使用 `trz` 命令上传文件，使用 `tsz /path/to/file` 命令下载文件。

- 有关 `trzsz` 更详细的文档，请查看 [https://trzsz.github.io/cn/](https://trzsz.github.io/cn/)。

## 使用建议

- 为了使用方便，可以设置 `alias` ，例如 `alias ssh="trzsz ssh"`，又或者 `alias ssh="trzsz -d ssh"` 支持拖文件上传。

- 如果在本地电脑使用 `tmux`，先不带 `trzsz` 运行 `tmux`，然后再使用 `trzsz ssh` 登录远程服务器。

## 可配置项

`trzsz` 使用的配置文件是 `~/.trzsz.conf`（ Windows 是 `C:\Users\your_name\.trzsz.conf` ）。注意路径必须包含 `/` 结尾，如：

```
DefaultUploadPath =
DefaultDownloadPath = /Users/username/Downloads/
```

- 如果 `DefaultUploadPath` 不为空，上传选择文件时会默认打开此目录。

- 如果 `DefaultDownloadPath` 不为空，下载文件时会自动下载到此目录（ 不需要再弹窗选择路径 ）。

## 支持 Zmodem

- 使用 `-z` 或 `--zmodem` 启用 `rz / sz` 功能，例如 `trzsz -z ssh remote_server`。

- 需要在客户端（ 本地电脑 ）上安装 `lrzsz`，例如 `brew install lrzsz`、`apt install lrzsz` 等。

- `trzsz --zmodem ssh xxx` 不兼容 Windows，你可以使用 [trzsz-ssh ( tssh )](https://trzsz.github.io/cn/ssh) 代替，如 `tssh --zmodem xxx`。

- 关于进度条，己传文件大小和传输速度不是精确值，会有一些偏差，它的主要作用只是指示传输正在进行中。

## 常见问题

- 如果 [MSYS2](https://www.msys2.org/) 或 [Git Bash](https://www.atlassian.com/git/tutorials/git-bash) 遇到错误 `The handle is invalid`。

  - 在 `trzsz` 前面加上 `winpty` 即可，如 `winpty trzsz ssh x.x.x.x` 这样登录服务器。
  - `Git Bash` 已默认安装有 `winpty`，`MSYS2` 需要手工安装 `pacman -S winpty`。

- 在 [MSYS2](https://www.msys2.org/) 和 [Cygwin](https://www.cygwin.com/) 等，不支持直接使用 `/usr/bin/ssh` 那个 `ssh`，可能会传输失败。需要使用 Windows 自带的 [OpenSSH](https://docs.microsoft.com/en-us/windows-server/administration/openssh/openssh_install_firstuse)。

  - `MSYS2` 中用法 `winpty trzsz /c/Windows/System32/OpenSSH/ssh.exe x.x.x.x`。
  - `Cygwin` 中用法 `trzsz "C:\Windows\System32\OpenSSH\ssh.exe" x.x.x.x`。
  - ⭐ 推荐使用 [trzsz-ssh](https://trzsz.github.io/cn/ssh) ( tssh )，`tssh` 等同于 `trzsz ssh`。

- 拖文件或目录到终端后，没有自动上传？

  - 检查 `ssh` 登录时有没有加上 `--dragfile` 选项，如 `trzsz --dragfile ssh x.x.x.x` 。
  - 检查服务器上有没有安装 [trzsz](https://trzsz.github.io/)，`trz` 应在某个 `PATH` 路径下。
  - 在 Windows 中，检查标题有没有`管理员`三个字，`以管理员运行` 时可能会由于 UAC 设置的原因导致拖不了文件。
  - 在 Windows Terminal 中，需要将文件或目录拖到左上角，显示`粘贴文件路径` 时再放开。
  - 在 Windows 的 `cmd` 和 `PowerShell` 中，一次只能拖一个文件或目录，拖多个也只有鼠标正对着的那个是生效的。

## 开发指引

想要让你自己开发的 ssh 客户端支持 trzsz ？请参考 [go ssh client example](https://github.com/trzsz/trzsz-go/blob/main/examples/ssh_client.go)。

## 录屏演示

#### Windows 系统

![windows trzsz ssh](https://trzsz.github.io/images/cmd_trzsz.gif)

#### Ubuntu 系统

![ubuntu trzsz ssh](https://trzsz.github.io/images/ubuntu_trzsz.gif)

#### 拖拽上传

![drag files ssh](https://trzsz.github.io/images/drag_files.gif)

## 联系方式

有什么问题可以发邮件给作者 <lonnywong@qq.com>，也可以提 [Issues](https://github.com/trzsz/trzsz-go/issues) 。欢迎加入 QQ 群：318578930。

## 赞助打赏

[❤️ 赞助 trzsz ❤️](https://github.com/trzsz)，请作者喝杯咖啡 ☕ ? 谢谢您们的支持！
