package ios

import "testing"

// THE_ANSWER_THAT_ARRIVES_AS_A_SEQUENCE — a profile names devices, and now the names are read. The fixture is
// the operator's evening, decoded: one profile covering Malta and NOT the second phone, whose udid Apple's own
// refusal named ("doesn't include the currently selected device … 00008110-0000C0FFEE0000B2").

const realProfilePlist = `<?xml version="1.0" encoding="UTF-8"?>
<plist version="1.0">
<dict>
	<key>Name</key>
	<string>iOS Team Provisioning Profile: com.example.ada.WebDriverAgentRunner.xctrunner</string>
	<key>TeamIdentifier</key>
	<array>
		<string>TEAM7QZ4X2</string>
	</array>
	<key>Entitlements</key>
	<dict>
		<key>application-identifier</key>
		<string>TEAM7QZ4X2.com.example.ada.WebDriverAgentRunner.xctrunner</string>
	</dict>
	<key>ProvisionedDevices</key>
	<array>
		<string>00008140-0000C0FFEE0000A1</string>
	</array>
	<key>ExpirationDate</key>
	<date>2026-09-01T00:00:00Z</date>
</dict>
</plist>`

func TestProvisionedDevicesAreReadFromTheSameDecodedPlist(t *testing.T) {
	got := devices(realProfilePlist)
	if len(got) != 1 || got[0] != "00008140-0000C0FFEE0000A1" {
		t.Fatalf("the device list is the profile's whole job and must be read: %v", got)
	}
	if devices("<plist><dict></dict></plist>") != nil {
		t.Fatal("a plist with no ProvisionedDevices array yields nil — 'restricts nothing', never an empty claim")
	}
}

func TestCoversDeviceIsPerDeviceFromOneFixture(t *testing.T) {
	p := Profile{
		Name:               "iOS Team Provisioning Profile: com.example.ada.WebDriverAgentRunner.xctrunner",
		AppID:              "TEAM7QZ4X2.com.example.ada.WebDriverAgentRunner.xctrunner",
		TeamID:             "TEAM7QZ4X2",
		ProvisionedDevices: devices(realProfilePlist),
	}

	// Malta is in the list; the operator's second phone — the one xcodebuild refused by name — is not.
	if !p.CoversDevice("00008140-0000C0FFEE0000A1") {
		t.Fatal("the covered sibling must report covered")
	}
	if p.CoversDevice("00008110-0000C0FFEE0000B2") {
		t.Fatal("the phone Apple refused by name must not be reported covered")
	}
	// Case-insensitive: Apple prints udids in either case.
	if !p.CoversDevice("00008140-0000c0ffee0000a1") {
		t.Fatal("udid comparison is case-insensitive")
	}
}

func TestAProfileWithNoDeviceListRestrictsNothing(t *testing.T) {
	p := Profile{AppID: "T.com.x", TeamID: "T"} // distribution/enterprise shape: no ProvisionedDevices at all
	if !p.CoversDevice("00008110-0000C0FFEE0000B2") {
		t.Fatal("no device list = restricts nothing; refusing a udid it never lists would invent a restriction")
	}
}

func TestMatchWalksEveryTeamProfileForTheDeviceAnswer(t *testing.T) {
	// The FIRST bundle+team match excludes the phone; a SECOND covers it. A device answer over ForTeam alone
	// would say missing about a machine that can sign for this phone.
	profiles := []Profile{
		{AppID: "T.com.x.xctrunner", TeamID: "T", ProvisionedDevices: []string{"OTHER"}},
		{AppID: "T.com.x.xctrunner", TeamID: "T", ProvisionedDevices: []string{"MINE"}},
	}
	m := Match(profiles, "com.x.xctrunner", "T")
	if len(m.ForTeamAll) != 2 {
		t.Fatalf("every bundle+team profile is the device question's population, got %d", len(m.ForTeamAll))
	}
	if !m.CoversDevice("MINE") || m.CoversDevice("NEITHER") {
		t.Fatalf("device coverage walks ALL matching profiles")
	}
}
