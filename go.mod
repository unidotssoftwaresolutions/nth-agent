// THE DECLARED PATH IS NOT A NAME WE CHOOSE — it is a measurement of where the repository is.
// Go module paths are case-sensitive, so a module declaring a path that differs from its repository
// by one letter answers every by-path fetch with "module declares its path as … but was required as
// …". RE-MEASURED 2026-09-21 from this repo's own remote, which had moved:
// github.com/unidotssoftwaresolutions/nth-agent. The 2026-08-21 measurement read the repository's old
// name and was true when it was taken; it is replaced rather than edited, because a sentence that claims to be a
// measurement and was rewritten by hand is no longer one.
//
// ⚠️ AND THE PATH IS IN THE BYTES, NOT ONLY IN THE SOURCE. Every function this module compiles carries
// it in its symbol — measured 2026-09-21 against the binary in this tree: 509 occurrences, and
// `go version -m` prints it outright. `-trimpath`, which release.yml passes, strips filesystem
// prefixes and leaves the module path alone. So the first panic on a customer's machine printed the
// old company name down every frame, and a published release would have carried it forever. That is
// what this rename is for, and why it could not wait for a quieter week.
module github.com/unidotssoftwaresolutions/nth-agent

go 1.27.0

require (
	github.com/andybalholm/brotli v1.2.6
	github.com/coder/websocket v1.8.15
	github.com/danielpaulus/go-ios v1.3.2
)

require (
	github.com/Masterminds/semver v1.5.0 // indirect
	github.com/cenkalti/backoff v2.2.1+incompatible // indirect
	github.com/google/uuid v1.1.2 // indirect
	github.com/grandcat/zeroconf v1.0.0 // indirect
	github.com/miekg/dns v1.1.57 // indirect
	go.mozilla.org/pkcs7 v0.9.0 // indirect
	golang.org/x/crypto v0.52.0 // indirect
	golang.org/x/net v0.55.0 // indirect
	golang.org/x/sys v0.45.0 // indirect
	golang.org/x/text v0.37.0 // indirect
	howett.net/plist v1.0.1 // indirect
	software.sslmate.com/src/go-pkcs12 v0.7.2 // indirect
)
