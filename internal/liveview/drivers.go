package liveview

import (
	"context"
	"regexp"
	"strconv"
	"strings"
)

// Driver is one Appium driver this machine has installed, by Appium's own list.
type Driver struct {
	Name                string
	Major, Minor, Patch int
}

// AtLeast reports whether this driver's version is major.minor.patch or later.
func (d Driver) AtLeast(major, minor, patch int) bool {
	if d.Major != major {
		return d.Major > major
	}
	if d.Minor != minor {
		return d.Minor > minor
	}
	return d.Patch >= patch
}

// Version is the driver's version as Appium printed it.
func (d Driver) Version() string {
	return strconv.Itoa(d.Major) + "." + strconv.Itoa(d.Minor) + "." + strconv.Itoa(d.Patch)
}

// Drivers is what this machine's Appium says it has installed — or, when it could not be asked, why not. An
// unread list is NOT an empty one: a judgement over it says "not checked", never "missing".
type Drivers struct {
	Read      bool
	Why       string // when !Read: why the list could not be read, in words
	Installed map[string]Driver
}

// Has returns the installed driver of that name.
func (d Drivers) Has(name string) (Driver, bool) {
	driver, ok := d.Installed[name]
	return driver, ok
}

// THE LINE APPIUM PRINTS PER INSTALLED DRIVER — "- uiautomator2@7.5.2 [installed (npm)]" (the doctor's own seal
// carries that shape from the operator's Mac). Colour codes are stripped first: Appium colours its list on a
// terminal, and an anchored pattern must not depend on whether stdout was one.
var (
	driverLine = regexp.MustCompile(`(?m)^\s*-\s*([A-Za-z0-9._-]+)@(\d+)\.(\d+)\.(\d+)`)
	colourCode = regexp.MustCompile(`\x1b\[[0-9;]*m`)
)

// ParseDriverList reads `appium driver list --installed`'s output into the drivers it names.
func ParseDriverList(out string) map[string]Driver {
	installed := map[string]Driver{}
	for _, m := range driverLine.FindAllStringSubmatch(colourCode.ReplaceAllString(out, ""), -1) {
		major, _ := strconv.Atoi(m[2])
		minor, _ := strconv.Atoi(m[3])
		patch, _ := strconv.Atoi(m[4])
		installed[m[1]] = Driver{Name: m[1], Major: major, Minor: minor, Patch: patch}
	}
	return installed
}

// ReadDrivers asks this machine's Appium which drivers it has — the same read-only command the doctor's driver
// checks run. lookPath and run are seams (exec.LookPath and a CombinedOutput runner in production).
//
// IT READS THE `appium` ON PATH, which is the installation the doctor reads too. The agent drives whatever Appium
// --appium names, and on a customer's machine that is this one; a machine whose server is some other install is told
// about this one, which the sentence's own words ("this machine's Appium") do not hide.
func ReadDrivers(ctx context.Context, lookPath func(string) (string, error),
	run func(ctx context.Context, name string, args ...string) (string, error)) Drivers {
	if _, err := lookPath("appium"); err != nil {
		return Drivers{Why: "Appium is not on PATH here, so its driver list could not be read"}
	}
	out, err := run(ctx, "appium", "driver", "list", "--installed")
	if err != nil {
		return Drivers{Why: "Appium's driver list could not be read (`appium driver list --installed`: " + firstLine(out, err) + ")"}
	}
	return Drivers{Read: true, Installed: ParseDriverList(out)}
}

func firstLine(out string, err error) string {
	for _, line := range strings.Split(colourCode.ReplaceAllString(out, ""), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			return line
		}
	}
	return err.Error()
}
