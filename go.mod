module github.com/trzsz/trzsz-go

go 1.18

replace github.com/trzsz/trzsz-go/trzsz => ../../trzsz

require (
	github.com/UserExistsError/conpty v0.1.0
	github.com/alexflint/go-arg v1.4.3
	github.com/creack/pty v1.1.18
	github.com/ncruces/zenity v0.8.8-0.20220617014907-176a3d48a105
	golang.org/x/sys v0.0.0-20220610221304-9f5ed59c137d
	golang.org/x/term v0.0.0-20220411215600-e5f449aeb171
	golang.org/x/text v0.3.7
)

require (
	github.com/akavel/rsrc v0.10.2 // indirect
	github.com/alexflint/go-scalar v1.1.0 // indirect
	github.com/dchest/jsmin v0.0.0-20220218165748-59f39799265f // indirect
	github.com/josephspurrier/goversioninfo v1.4.0 // indirect
	github.com/randall77/makefat v0.0.0-20210315173500-7ddd0e42c844 // indirect
	golang.org/x/image v0.0.0-20220601225756-64ec528b34cd // indirect
)

replace github.com/alexflint/go-arg v1.4.3 => github.com/trzsz/go-arg v1.4.4-0.20220603094444-443c2e23b974
