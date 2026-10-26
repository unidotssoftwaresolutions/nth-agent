package android

// PerTickRead is one adb round trip this probe makes FOR EVERY ATTACHED DEVICE, EVERY TICK — and what it costs to
// keep making it.
type PerTickRead struct {
	// Value is the read, spelled the way probe.go spells it, because the census discovers this population from
	// that source rather than from this list.
	Value string
	// Cached is whether it is read once per attached device instead of every tick.
	Cached bool
	// Changes names what would have to happen for the answer to differ — the whole argument for caching or not.
	Changes string
	// Cost is the measured round trip on the operator's Samsung over USB, 2026-08-22.
	Cost string
}

// PerTickReads — EVERY VALUE THIS PROBE RE-MEASURES THAT CANNOT CHANGE BETWEEN TICKS.
//
// The agent ticks every 10s (DefaultInterval = NodeTtlSeconds/3) under a 20s per-probe budget. Before this slice a
// single attached device cost SEVEN adb round trips per tick — four property reads plus three `settings get` — for
// answers that change only when a person changes something. On a rack of ten phones that is 70 sequential round
// trips every ten seconds; at the measured costs below, ~5.7s of every 10s tick, and a phone under load answering
// three times slower would push the probe past its own 20s budget and turn a healthy rack into "adb could not be
// asked". A constant re-measured forever is not free; it is a timeout waiting for a busy afternoon.
var PerTickReads = []PerTickRead{
	{
		Value:   "settings get {global,secure,system} verifier_verify_adb_installs",
		Cached:  true,
		Changes: "a person opens Settings and flips a switch — which needs the phone unlocked and, to take effect for adb, a re-plug or reboot; both take the device out of the attached set, which is exactly what evicts the entry",
		Cost:    "3 × ~104ms = ~312ms per device per tick, now paid once per attachment",
	},
	{
		Value:   "getprop ro.product.model",
		Cached:  false,
		Changes: "nothing short of a different device on the same serial",
		Cost:    "~64ms",
	},
	{
		Value:   "getprop ro.product.manufacturer",
		Cached:  false,
		Changes: "nothing short of a different device on the same serial",
		Cost:    "~64ms",
	},
	{
		Value:   "getprop ro.build.version.release",
		Cached:  false,
		Changes: "an OS upgrade, which reboots the device out of the attached set",
		Cost:    "~64ms",
	},
	{
		Value:   "getprop ro.build.characteristics",
		Cached:  false,
		Changes: "nothing short of a different device on the same serial",
		Cost:    "~64ms",
	},
}

// WHY THE FOUR PROPERTY READS ARE STILL PAID EVERY TICK, stated rather than left as an oversight.
//
// They are as standing as the setting above, and the same cache would hold them. What stops this slice from doing
// it is what the cache must refuse to remember: A FAILED READ IS NOT A FACT. getprop swallows an error into an
// empty string, and those four fields ARE THE ROW A LEASE IS FILTERED ON — publishing a device is not offering it,
// and an empty Model is a device nobody can ask for. Caching the read means caching the emptiness for as long as
// the device stays attached, turning one transient adb hiccup into a device that cannot be leased until somebody
// unplugs it. Today's per-tick read self-heals on the next tick.
//
// So the fix is not "cache them too": it is to teach describe() the difference between "adb answered, and the
// answer is empty" and "adb did not answer" — the same Asked distinction InstallConfirmation now carries — and
// then cache only the former. That is a real change to the descriptor path with its own seals, and this slice
// leaves it named rather than half-done. The saving it would buy is the remaining ~256ms per device per tick.
