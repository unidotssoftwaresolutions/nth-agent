# iOS probe fixtures

The iOS source is a Go library (go-ios v1.3.2), not a text-printing binary, so these fixtures are the
library's OBSERVED BEHAVIOUR rather than captured stdout.

**CAPTURED on this machine (macOS 26.5.1, go-ios v1.3.2, 2026-08-21) by calling `ios.ListDevices()`:**

- healthy daemon, nothing attached → `(list, nil)` with `len(DeviceList) == 0`.
  *An answer: zero devices.* Modelled by `Lister` returning `(nil, nil)`.
- `USBMUXD_SOCKET_ADDRESS=/nonexistent/usbmuxd` → error
  `dial unix /nonexistent/usbmuxd: connect: no such file or directory`.
  *Not an answer at all.* Modelled by `Lister` returning that exact error string.

**INVENTED — no iPhone is attached to this machine.** Each names the case it stands for; the state
strings are the vocabulary go-ios's pairing layer uses (`PairingDialogResponsePending` appears in its
source, lower-cased here as this repo stores states):

- a trusted device (state `""`) — offerable.
- `pairingdialogresponsepending` — the Trust dialog is waiting on the device.
- `passwordprotected` — the device is locked.
- `warpdriveengaged` — a state no go-ios version prints, standing for "a future version prints a word
  this classifier has never seen". It must survive verbatim, never be flattened to "other".
