# trzsz-go
[trzsz](https://github.com/trzsz/trzsz) ( trz / tsz ) is a simple file transfer tools, similar to lrzsz ( rz / sz ), and compatible with tmux.

[![MIT License](https://img.shields.io/badge/license-MIT-green.svg?style=flat)](https://choosealicense.com/licenses/mit/)
[![GitHub Release](https://img.shields.io/github/v/release/trzsz/trzsz-go)](https://github.com/trzsz/trzsz-go/releases)

***Please check [https://github.com/trzsz/trzsz](https://github.com/trzsz/trzsz) for more information of `trzsz`.***

`trzsz-go` is the `go` version of `trzsz`, supports native terminals that support a local shell.


## Installation

### with apt on Ubuntu

```sh
sudo apt update && sudo apt install software-properties-common
sudo add-apt-repository ppa:trzsz/ppa && sudo apt update
sudo apt install trzsz
```

### with apt on Debian
```
sudo apt install curl gpg
curl -s 'https://keyserver.ubuntu.com/pks/lookup?op=get&search=0x7074ce75da7cc691c1ae1a7c7e51d1ad956055ca' \
    | gpg --dearmor -o /usr/share/keyrings/trzsz.gpg
echo 'deb [signed-by=/usr/share/keyrings/trzsz.gpg] https://ppa.launchpadcontent.net/trzsz/ppa/ubuntu jammy main' \
    | sudo tee /etc/apt/sources.list.d/trzsz.list
sudo apt update
sudo apt install trzsz
```


### with yum

```
echo '[trzsz]
name=Trzsz Repo
baseurl=https://yum.fury.io/trzsz/
enabled=1
gpgcheck=0' | sudo tee /etc/yum.repos.d/trzsz.repo

sudo yum install trzsz
```


### with [homebrew](https://brew.sh/)

```
brew update
brew install trzsz-go
```


### with [scoop](https://scoop.sh/) on Windows

```
scoop bucket add extras
scoop install trzsz
```


### with [yay](https://github.com/Jguer/yay) on ArchLinux

```
yay -Syu
yay -S trzsz
```


### Others

Download from the github [releases](https://github.com/trzsz/trzsz-go/releases), or install from the source code:

```sh
git clone https://github.com/trzsz/trzsz-go.git
cd trzsz-go
make
sudo make install
```


## Usage

### on the local computer

Add `trzsz` before the shell to support trzsz ( trz / tsz ), e.g.:

```sh
trzsz bash
trzsz PowerShell
trzsz ssh x.x.x.x
```

Add `trzsz --dragfile` before the `ssh` to enable drag files and directories to upload, e.g.:

```sh
trzsz -d ssh x.x.x.x
trzsz --dragfile ssh x.x.x.x
```


### on the jump server

If using `tmux` on the jump server, use `trzsz --relay ssh` to login to the remote server, e.g.:

```sh
tmux
trzsz -r ssh x.x.x.x
trzsz --relay ssh x.x.x.x
```


### on the remote server

Similar to lrzsz ( rz / sz ), command `trz` to upload files, command `tsz /path/to/file` to download files.

For more information, check the website of trzsz: [https://trzsz.github.io](https://trzsz.github.io/).


## Suggestion

* It is recommended to set `alias ssh="trzsz ssh"` for convenience, `alias ssh="trzsz -d ssh"` for dragging files.

* If using `tmux` on the local computer, run `tmux` ( without `trzsz` ) first, then `trzsz ssh` to login.


## Configuration

`trzsz` looks for configuration at `~/.trzsz.conf`. e.g.:

```
DefaultUploadPath =
DefaultDownloadPath = /Users/username/Downloads/
```

* If the `DefaultUploadPath` is not empty, the path will be opened by default while choosing upload files.

* If the `DefaultDownloadPath` is not empty, downloading files will be saved to the path automatically instead of asking each time.


## Trouble shooting

* If using [MSYS2](https://www.msys2.org/) or [Git Bash](https://www.atlassian.com/git/tutorials/git-bash) on windows, and getting an error `The handle is invalid`.
  * Install [winpty](https://github.com/rprichard/winpty) by `pacman -S winpty` in `MSYS2`.
  * `Git Bash` should have winpty installed, no need to install it manually.
  * Add `winpty` before `trzsz`, e.g.: `winpty trzsz ssh x.x.x.x`.

* The `/usr/bin/ssh` in [MSYS2](https://www.msys2.org/) and [Cygwin](https://www.cygwin.com/) is not supported yet, use the [OpenSSH](https://docs.microsoft.com/en-us/windows-server/administration/openssh/openssh_install_firstuse) instead.
  * In `MSYS2`, e.g.: `winpty trzsz /c/Windows/System32/OpenSSH/ssh.exe x.x.x.x`.
  * In `Cygwin`, e.g.: `trzsz "C:\Windows\System32\OpenSSH\ssh.exe" x.x.x.x`.

* Dragging files doesn't upload?
  * Don't forget the `--dragfile` option. e.g.: `trzsz -d ssh x.x.x.x`.
  * Make sure the `trz` in one of the `PATH` directory on the server.
  * On Windows, make sure there is no `Administrator` on the title.
  * The `cmd` and `PowerShell` only support draging one file into it.
  * On the Windows Terminal, drag files to the top left where shows `Paste path to file`.


## Screenshot

#### Windows

  ![windows trzsz ssh](https://trzsz.github.io/images/cmd_trzsz.gif)


#### Ubuntu

  ![ubuntu trzsz ssh](https://trzsz.github.io/images/ubuntu_trzsz.gif)


#### Drag files

  ![drag files ssh](https://trzsz.github.io/images/drag_files.gif)


## Contact

Feel free to email me <lonnywong@qq.com>. Welcome to join the QQ group: 318578930.
