# Android parser fixtures

**CAPTURED verbatim on this machine (macOS, adb 1.0.41 / Version 36.0.2-14143358, 2026-08-21):**

- `empty.txt` — `adb devices -l` with nothing attached. The whole output is a header line and a blank
  line. This is the ANSWERED-with-zero case, and the reason it must never be confused with a failure.
- `server-not-acking.txt` — `ANDROID_ADB_SERVER_PORT=1 adb devices -l`, i.e. adb unable to reach or
  start a server. Exit code **1**, and the text below is what it printed. The COULD-NOT-ASK case. The account name in its paths and the
  temporary directory's name are replaced by invented ones of the same shape; nothing else is changed.

**INVENTED — no such handset/host is attached to this machine.** Each names the case it stands for and
follows the documented `adb devices -l` line shape (`<serial> <state> [key:value …]`):

- `attached-mixed.txt` — one usable device, one `unauthorized` (the Samsung-at-the-authorize-dialog
  case that cost two runs), one `offline`, one `no permissions` (a Linux udev host).
- `unknown-state.txt` — a state no adb version here prints, standing for "a future adb prints a word
  this parser has never seen". It must survive verbatim, never be flattened to "other".
