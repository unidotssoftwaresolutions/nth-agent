package connect_test

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/unidotssoftwaresolutions/nth-agent/internal/allowed"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/connect"
	"github.com/unidotssoftwaresolutions/nth-agent/internal/wda"
)

// WHAT_THE_FIRST_TRY_SHOWED, decision A6: the WebDriverAgent on the phone is the one this machine declared. The port declared
// for an iPhone is the port on the phone the driver reaches WebDriverAgent on, and the agent stops WebDriverAgent there
// after five idle minutes; another app on the phone could then hold it and be taken for WebDriverAgent by the next session.
// So before the port is declared, before each session is created, and once the session has started, the agent asks
// what answers there. The phone's answers here are a fake's: no phone is touched.

const (
	theIPhone   = "00008030-HEALTHY" // driverOutcomes' iPhone
	itsPort     = 21888
	theBundle   = "com.example.WebDriverAgentRunner" // drivingConfig's
	wdaSession  = "6C1E2A9B-0000-4000-8000-00000000AB12"
	strangerSay = "says it is the WebDriverAgent build \"com.other.Thing\""
)

func iPhoneCreate() []byte {
	return []byte(`{"capabilities":{"alwaysMatch":{"platformName":"iOS","appium:automationName":"XCUITest",` +
		`"appium:udid":"` + theIPhone + `","appium:wdaLocalPort":21888}}}`)
}

// answering is a phone whose WebDriverAgent port answers what the test puts in it, and counts each ask.
type answering struct {
	now   atomic.Value // wda.Identity
	asked atomic.Int64
	port  atomic.Int64
}

func (a *answering) set(id wda.Identity) { a.now.Store(id) }
func (a *answering) ask(_ context.Context, udid string, port uint16) wda.Identity {
	a.asked.Add(1)
	a.port.Store(int64(port))
	if udid != theIPhone {
		return wda.Identity{Is: wda.IdentityNothing}
	}
	return a.now.Load().(wda.Identity)
}

func identityConfig(t *testing.T, phone *answering) (connect.Config, *stubFarm, *strings.Builder, *allowed.Phones) {
	stub, server := newStubFarm(t)
	out := &strings.Builder{}
	cfg := drivingConfig(server, config(server, out, 1))
	phones := &allowed.Phones{}
	cfg.Phones = phones
	cfg.WdaPortFor = func(context.Context, string) uint16 { return itsPort }
	cfg.WdaIdentity = phone.ask
	return cfg, stub, out, phones
}

func TestAnIphoneWhosePortAStrangerAnswersIsNotDeclaredAndSaysWhy(t *testing.T) {
	phone := &answering{}
	phone.set(wda.Identity{Is: wda.IdentityStranger, Bundle: "com.other.Thing", Found: strangerSay})
	cfg, stub, out, phones := identityConfig(t, phone)
	if err := connect.Run(context.Background(), cfg); err != nil {
		t.Fatalf("run: %v", err)
	}
	heartbeat := stub.lastHeartbeat.Load().([]byte)
	if _, offered := descriptors(t, heartbeat)[theIPhone]; offered || phones.Offers(theIPhone) {
		t.Fatalf("an iPhone whose WebDriverAgent port a stranger answers must not be offered, nor its port declared:\n%s", heartbeat)
	}
	if phone.port.Load() != itsPort {
		t.Fatalf("the question is asked on the port this machine declares (%d), got %d", itsPort, phone.port.Load())
	}
	said := strings.Join(strings.Fields(out.String()), " ")
	for _, must := range []string{theIPhone, "is not the WebDriverAgent this machine declared (" + theBundle + ")", strangerSay} {
		if !strings.Contains(said, must) {
			t.Fatalf("the terminal must say %q:\n%s", must, out.String())
		}
		if !strings.Contains(string(heartbeat), strings.ReplaceAll(must, `"`, `\"`)) {
			t.Fatalf("the farm's answer must say %q:\n%s", must, heartbeat)
		}
	}
}

func TestAnIphoneWhosePortIsItsOwnWebDriverAgentOrNothingIsDeclaredAsBefore(t *testing.T) {
	for _, now := range []wda.Identity{{Is: wda.IdentityDeclared, Bundle: theBundle}, {Is: wda.IdentityNothing}, {Is: wda.IdentityNotAsked, Found: "the phone could not be reached"}} {
		phone := &answering{}
		phone.set(now)
		cfg, stub, _, phones := identityConfig(t, phone)
		if err := connect.Run(context.Background(), cfg); err != nil {
			t.Fatalf("run: %v", err)
		}
		row, offered := descriptors(t, stub.lastHeartbeat.Load().([]byte))[theIPhone]
		if !offered || row["wdaPort"] != float64(itsPort) || !phones.Offers(theIPhone) {
			t.Fatalf("%+v: the iPhone is offered with its port as before, got %v", now, row)
		}
	}
}

// BEFORE EACH SESSION: the port is asked again as the create arrives, since an idle stop may have freed it since.
func TestASessionIsRefusedWhenAStrangerAnswersOnItsPortAsItStarts(t *testing.T) {
	phone := &answering{}
	phone.set(wda.Identity{Is: wda.IdentityDeclared, Bundle: theBundle})
	cfg, _, _, phones := identityConfig(t, phone)
	if err := connect.Run(context.Background(), cfg); err != nil {
		t.Fatalf("run: %v", err)
	}
	if refusal := allowed.Create(iPhoneCreate(), "https://farm.nthcase.com", phones); refusal != "" {
		t.Fatalf("a create while the declared WebDriverAgent answers passes as before, got %q", refusal)
	}
	for _, now := range []wda.Identity{
		{Is: wda.IdentityStranger, Bundle: "com.other.Thing", Found: strangerSay},
		{Is: wda.IdentityNotAsked, Found: "the phone could not be reached"},
	} {
		phone.set(now)
		refusal := allowed.Create(iPhoneCreate(), "https://farm.nthcase.com", phones)
		if !strings.Contains(refusal, theIPhone) || !strings.Contains(refusal, "nothing was sent to Appium") {
			t.Fatalf("%+v: the create must be refused in words, before Appium, got %q", now, refusal)
		}
	}
}

// ONCE THE SESSION HAS STARTED: the session the driver talks to must be on the declared WebDriverAgent, which must hold one.
func TestASessionThatStartedIsCheckedForTheWebDriverAgentItTalksTo(t *testing.T) {
	phone := &answering{}
	phone.set(wda.Identity{Is: wda.IdentityDeclared, Bundle: theBundle})
	cfg, _, _, phones := identityConfig(t, phone)
	if err := connect.Run(context.Background(), cfg); err != nil {
		t.Fatalf("run: %v", err)
	}
	for _, c := range []struct {
		name string
		now  wda.Identity
		ok   bool
	}{
		{"the declared WebDriverAgent holds the session", wda.Identity{Is: wda.IdentityDeclared, Bundle: theBundle, SessionID: wdaSession}, true},
		{"the declared WebDriverAgent holds no session", wda.Identity{Is: wda.IdentityDeclared, Bundle: theBundle}, false},
		{"a stranger answers", wda.Identity{Is: wda.IdentityStranger, Found: strangerSay}, false},
		{"nothing answers", wda.Identity{Is: wda.IdentityNothing}, false},
		{"the phone could not be asked", wda.Identity{Is: wda.IdentityNotAsked, Found: "the phone could not be reached"}, false},
	} {
		phone.set(c.now)
		why := phones.Started(context.Background(), iPhoneCreate())
		if (why == "") != c.ok || (!c.ok && !strings.Contains(why, theIPhone)) {
			t.Errorf("%s: got %q", c.name, why)
		}
	}
	if why := phones.Started(context.Background(), []byte(`{"capabilities":{"alwaysMatch":{"platformName":"Android","appium:udid":"R5CT30ABCDE"}}}`)); why != "" {
		t.Errorf("an Android session is not asked about WebDriverAgent, got %q", why)
	}
}
