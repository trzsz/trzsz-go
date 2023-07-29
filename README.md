# trzsz-go

[trzsz](https://trzsz.github.io/) ( trz / tsz ) is a simple file transfer tools, similar to lrzsz ( rz / sz ), and compatible with tmux.

Website: [https://trzsz.github.io/go](https://trzsz.github.io/go) 　中文文档：[https://trzsz.github.io/cn/go](https://trzsz.github.io/cn/go)

[![MIT License](https://img.shields.io/badge/license-MIT-green.svg?style=flat)](https://choosealicense.com/licenses/mit/)
[![GitHub Release](https://img.shields.io/github/v/release/trzsz/trzsz-go)](https://github.com/trzsz/trzsz-go/releases)

**_Please check [https://trzsz.github.io](https://trzsz.github.io) for more information of `trzsz`._**

`trzsz-go` is the `go` version of `trzsz`, supports native terminals that support a local shell.

⭐ It's recommended to use the `go` version of `trzsz` on the server.

## Installation

- Install with apt on Ubuntu

  <details><summary><code>sudo apt install trzsz</code></summary>

  ```sh
  sudo apt update && sudo apt install software-properties-common
  sudo add-apt-repository ppa:trzsz/ppa && sudo apt update

  sudo apt install trzsz
  ```

  </details>

- Install with apt on Debian

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

- Install with yum on Linux

  <details><summary><code>sudo yum install trzsz</code></summary>

  - Install with [gemfury](https://gemfury.com/) repository.

    ```sh
    echo '[trzsz]
    name=Trzsz Repo
    baseurl=https://yum.fury.io/trzsz/
    enabled=1
    gpgcheck=0' | sudo tee /etc/yum.repos.d/trzsz.repo

    sudo yum install trzsz
    ```

  - Install with [wlnmp](https://www.wlnmp.com/install) repository. It's not necessary to configure the epel repository for trzsz, take CentOS as an example:

    ```sh
    sudo rpm -ivh https://mirrors.wlnmp.com/centos/wlnmp-release-centos.noarch.rpm

    sudo yum install trzsz
    ```

  </details>

- Install with [yay](https://github.com/Jguer/yay) on ArchLinux

  <details><summary><code>yay -S trzsz</code></summary>

  ```sh
  yay -Syu
  yay -S trzsz
  ```

  </details>

- Install with [homebrew](https://brew.sh/) on MacOS

  <details><summary><code>brew install trzsz-go</code></summary>

  ```sh
  brew update
  brew install trzsz-go
  ```

  </details>

- Install with [scoop](https://scoop.sh/) / [winget](https://learn.microsoft.com/en-us/windows/package-manager/winget/) / [choco](https://community.chocolatey.org/) on Windows

  <details><summary><code>scoop install trzsz</code> / <code>winget install trzsz</code> / <code>choco install trzsz</code></summary>

  ```sh
  scoop bucket add extras
  scoop install trzsz
  ```

  ```sh
  winget install trzsz
  ```

  ```sh
  choco install trzsz
  ```

  </details>

- Install with Go ( Requires go 1.20 or later )

  <details><summary><code>go install github.com/trzsz/trzsz-go/cmd/...@latest</code></summary>

  ```sh
  go install github.com/trzsz/trzsz-go/cmd/trz@latest
  go install github.com/trzsz/trzsz-go/cmd/tsz@latest
  go install github.com/trzsz/trzsz-go/cmd/trzsz@latest
  ```

  The binaries are usually located in `~/go/bin/` ( `C:\Users\your_name\go\bin\` on Windows ).

  </details>

- Download from the [Releases](https://github.com/trzsz/trzsz-go/releases)

  <details><summary><code>Or build and install from the source code ( Requires go 1.20 or later )</code></summary>

  ```sh
  git clone https://github.com/trzsz/trzsz-go.git
  cd trzsz-go
  make
  sudo make install
  ```

  </details>

## Usage

### Use on the local computer

- Add `trzsz` before the shell to support trzsz ( trz / tsz ), e.g.:

  ```sh
  trzsz bash
  trzsz PowerShell
  trzsz ssh x.x.x.x
  ```

- Add `trzsz --dragfile` before the `ssh` to enable drag files and directories to upload, e.g.:

  ```sh
  trzsz -d ssh x.x.x.x
  trzsz --dragfile ssh x.x.x.x
  ```

### Use on the jump server

- If using `tmux` on the jump server, use `trzsz --relay ssh` to login to the remote server, e.g.:

  ```sh
  trzsz ssh jump_server
  tmux
  trzsz --relay ssh remote_server
  ```

### Use on the remote server

- Similar to lrzsz ( rz / sz ), command `trz` to upload files, command `tsz /path/to/file` to download files.

- For more information, check the website of trzsz: [https://trzsz.github.io](https://trzsz.github.io/). 中文文档：[https://trzsz.github.io/cn/](https://trzsz.github.io/cn/)

## Suggestion

- It is recommended to set `alias ssh="trzsz ssh"` for convenience, `alias ssh="trzsz -d ssh"` for dragging files to upload.

- If using `tmux` on the local computer, run `tmux` ( without `trzsz` ) first, then `trzsz ssh` to login.

## Configuration

`trzsz` looks for configuration at `~/.trzsz.conf` ( `C:\Users\your_name\.trzsz.conf` on Windows ). The path have to end with `/`, e.g.:

```
DefaultUploadPath =
DefaultDownloadPath = /Users/username/Downloads/
```

- If the `DefaultUploadPath` is not empty, the path will be opened by default while choosing upload files.

- If the `DefaultDownloadPath` is not empty, downloading files will be saved to the path automatically instead of asking each time.

## Trouble shooting

- If using [MSYS2](https://www.msys2.org/) or [Git Bash](https://www.atlassian.com/git/tutorials/git-bash) on windows, and getting an error `The handle is invalid`.

  - Install [winpty](https://github.com/rprichard/winpty) by `pacman -S winpty` in `MSYS2`.
  - `Git Bash` should have winpty installed, no need to install it manually.
  - Add `winpty` before `trzsz`, e.g.: `winpty trzsz ssh x.x.x.x`.

- The `/usr/bin/ssh` in [MSYS2](https://www.msys2.org/) and [Cygwin](https://www.cygwin.com/) is not supported yet, use the [OpenSSH](https://docs.microsoft.com/en-us/windows-server/administration/openssh/openssh_install_firstuse) instead.

  - In `MSYS2`, e.g.: `winpty trzsz /c/Windows/System32/OpenSSH/ssh.exe x.x.x.x`.
  - In `Cygwin`, e.g.: `trzsz "C:\Windows\System32\OpenSSH\ssh.exe" x.x.x.x`.
  - ⭐ Recommended to use [trzsz-ssh](https://trzsz.github.io/ssh) ( tssh ) instead, `tssh` is same as `trzsz ssh`.

- Dragging files doesn't upload?
  - Don't forget the `--dragfile` option. e.g.: `trzsz -d ssh x.x.x.x`.
  - Make sure the `trz` in one of the `PATH` directory on the server.
  - On Windows, make sure there is no `Administrator` on the title.
  - The `cmd` and `PowerShell` only support draging one file into it.
  - On the Windows Terminal, drag files to the top left where shows `Paste path to file`.

## Development

Want to write your own ssh client that supports trzsz? Please check the [go ssh client example](https://github.com/trzsz/trzsz-go/blob/main/examples/ssh_client.go).

## Screenshot

#### Windows

![windows trzsz ssh](https://trzsz.github.io/images/cmd_trzsz.gif)

#### Ubuntu

![ubuntu trzsz ssh](https://trzsz.github.io/images/ubuntu_trzsz.gif)

#### Drag files

![drag files ssh](https://trzsz.github.io/images/drag_files.gif)

## Contact

Feel free to email the author <lonnywong@qq.com>, or create an [issue](https://github.com/trzsz/trzsz-go/issues). Welcome to join the QQ group: 318578930.

## Sponsor

Want to buy the author a drink 🍺 ?

![sponsor wechat qrcode](https://trzsz.github.io/images/sponsor_wechat.jpg)
![sponsor alipay qrcode](https://trzsz.github.io/images/sponsor_alipay.jpg)

Thanks [@BrightXiaoHan](https://github.com/BrightXiaoHan) [@pmzgit](https://github.com/pmzgit).
