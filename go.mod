module github.com/trzsz/trzsz-go

go 1.13

replace github.com/trzsz/trzsz-go/trzsz => ../../trzsz

require (
	github.com/UserExistsError/conpty v0.1.0
	github.com/alexflint/go-arg v1.4.3
	github.com/creack/pty v1.1.18
	github.com/ncruces/zenity v0.8.6
	golang.org/x/sys v0.0.0-20220513210249-45d2b4557a2a
	golang.org/x/term v0.0.0-20220411215600-e5f449aeb171
	golang.org/x/text v0.3.7
)

replace github.com/alexflint/go-arg v1.4.3 => github.com/trzsz/go-arg v1.4.4-0.20220603094444-443c2e23b974
