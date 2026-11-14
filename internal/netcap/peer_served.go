package netcap

// ── WHO MAY SERVE THIS ACCOUNT: A PROGRAM THIS PROCESS DIALS ON THIS MACHINE (ANOTHER_ACCOUNT_AND_LIMITS) ────────────
//
// The kernel names the account that MADE a socket, not the account whose program holds it now. For a connection to the
// capture proxy that is the same question (peer.go); for a program the agent dials — Appium, adb's server, a phone's
// screen server, a farm on loopback — it is not, when root made the listening socket and handed it on:
//   - on a Mac, launchd runs as root and makes the listening socket a LaunchAgent asks for, and any account can install a
//     LaunchAgent of its own. So a socket root made may be served by a program another account started, and it is not
//     taken for this account's.
//   - on Linux and Windows, a socket root or LocalSystem made reaches another account's program only by an administrator's
//     own doing (a socket unit, a service), and an adb server started with sudo on Linux runs as root. Root is let through
//     there, as it is for the capture proxy.

// MayServeThisAccount says whether a program this process dialled, whose socket the kernel says uid made, may be used
// as this account's.
func MayServeThisAccount(uid int) bool {
	return uid == ThisAccount() || (uid == 0 && rootMakesOnlyItsOwn)
}

// RootServesNote is why a socket root made is not taken for this account's program on a Mac, in a refusal's words.
const RootServesNote = "and on a Mac root's launchd makes such a socket for a program any account on this machine can start"
