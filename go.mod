module github.com/trzsz/trzsz-go

go 1.18

replace github.com/trzsz/trzsz-go/trzsz => ../../trzsz

require (
	github.com/UserExistsError/conpty v0.1.0
	github.com/alexflint/go-arg v1.4.3
	github.com/creack/pty v1.1.18
	github.com/ncruces/zenity v0.10.0
	golang.org/x/sys v0.2.0
	golang.org/x/term v0.2.0
	golang.org/x/text v0.4.0
)

require (
	github.com/akavel/rsrc v0.10.2 // indirect
	github.com/alexflint/go-scalar v1.2.0 // indirect
	github.com/dchest/jsmin v0.0.0-20220218165748-59f39799265f // indirect
	github.com/josephspurrier/goversioninfo v1.4.0 // indirect
	github.com/randall77/makefat v0.0.0-20210315173500-7ddd0e42c844 // indirect
	golang.org/x/image v0.1.0 // indirect
)

replace github.com/alexflint/go-arg v1.4.3 => github.com/trzsz/go-arg v1.4.4-0.20220722153732-ac5a9f75703f
