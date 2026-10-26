// Package nthagent is this module's root, and it holds one thing: THE THIRD-PARTY NOTICES that the licences of the code
// inside the agent ask to travel with every copy of it — so that the binary carries them itself, and `nth-agent notices`
// prints them on any machine it runs on.
//
// IT IS HERE, AND NOT BESIDE cmd/nth-agent, BECAUSE go:embed READS ONLY FILES AT OR BELOW ITS OWN PACKAGE'S DIRECTORY, and
// the file belongs at the repository root: it is the one file the release attaches beside its archives and puts inside each
// of them, and the npm package ships beside its README. One file, read by every one of those, so none can carry another list.
//
// THE FILE IS GENERATED, NEVER EDITED BY HAND. notices_test.go builds it from what the release's programs link, on every
// platform the release builds, and fails when the file in the tree is not exactly what it would build. To rewrite it:
//
//	go test . -run TestTheNoticesAreWhatTheReleaseLinks -update
//
// and then read the diff.
package nthagent

import _ "embed"

// ThirdPartyNotices is THIRD_PARTY_NOTICES.txt, byte for byte.
//
//go:embed THIRD_PARTY_NOTICES.txt
var ThirdPartyNotices string
