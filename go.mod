module github.com/trzsz/trzsz-go

go 1.20

require (
	github.com/UserExistsError/conpty v0.1.0
	github.com/alexflint/go-arg v1.4.3
	github.com/creack/pty v1.1.18
	github.com/klauspost/compress v1.16.5
	github.com/ncruces/zenity v0.10.8
	github.com/stretchr/testify v1.7.0
	golang.org/x/sys v0.8.0
	golang.org/x/term v0.8.0
	golang.org/x/text v0.9.0
)

require (
	github.com/akavel/rsrc v0.10.2 // indirect
	github.com/alexflint/go-scalar v1.2.0 // indirect
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/dchest/jsmin v0.0.0-20220218165748-59f39799265f // indirect
	github.com/josephspurrier/goversioninfo v1.4.0 // indirect
	github.com/pmezard/go-difflib v1.0.0 // indirect
	github.com/randall77/makefat v0.0.0-20210315173500-7ddd0e42c844 // indirect
	golang.org/x/image v0.7.0 // indirect
	gopkg.in/yaml.v3 v3.0.0-20200313102051-9f266ea9e77c // indirect
)

replace github.com/trzsz/trzsz-go => ../..

replace github.com/alexflint/go-arg v1.4.3 => github.com/trzsz/go-arg v1.4.4-0.20220722153732-ac5a9f75703f
