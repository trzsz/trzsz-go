# trzsz-go
[trzsz](https://github.com/trzsz/trzsz) ( trz / tsz ) is a simple file transfer tools, similar to lrzsz ( rz / sz ), and compatible with tmux.


## Installation

### on Ubuntu
*Not released yet, please download the latest [release](https://github.com/trzsz/trzsz-go/releases) from GitHub.*

```sh
sudo add-apt-repository ppa:trzsz/ppa
sudo apt update
sudo apt install trzsz
```


### on Windows / macOS / Other

Please download the latest [release](https://github.com/trzsz/trzsz-go/releases) from GitHub.


### install from Source Code

```sh
git clone https://github.com/trzsz/trzsz-go.git
cd trzsz-go
make
sudo make install
```


## Usage

### on Local

Install the `trzsz` binary to one of the `PATH` directory.

Add `trzsz` before the shell to support trzsz ( trz / tsz ), e.g.:

```sh
trzsz bash
trzsz.exe cmd
trzsz ssh x.x.x.x
```

Add `trzsz --dragfile` before the `ssh` to enable drag files to upload, e.g.:

```sh
trzsz -d ssh x.x.x.x
trzsz --dragfile ssh x.x.x.x
```


### on Server

Install the `trz` and `tsz` binaries to one of the `PATH` directory.

Similar to lrzsz ( rz / sz ), command `trz` to upload files, command `tsz /path/to/file` to download files.

For more information, see the website of trzsz: [https://trzsz.github.io](https://trzsz.github.io/).


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

Feel free to email me <lonnywong@qq.com>.
