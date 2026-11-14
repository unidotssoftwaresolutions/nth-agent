package tunnel

// appiumIsSensitive is THE ONE HEADER OF A FRAME'S THIS AGENT PASSES ON BESIDE Content-Type (THE_HEADER_THAT_REACHES_APPIUM).
//
// WHAT IT DOES. The platform sets X-Appium-Is-Sensitive on each request that carries a value typed into the app under test — a
// saved login, a person's recorded text, the model's text — and the farm carries it in the frame's headers. Appium (base driver
// 10.6.0, handleLogContext) reads it on that request alone and writes the parts of that request's lines it marks sensitive as
// **SECURE** in its own log: the request's body, the arguments a command is called with, the body a driver passes on. This agent
// forwarded Content-Type alone, so every saved login typed on a phone on this computer was written in clear in the log of the
// Appium the customer runs here.
//
// WHY IT IS SAFE TO LET THROUGH. It changes only what Appium writes in its own log for the request that carries it: not what
// Appium does, not which session or phone the request reaches, not the answer. Everything this agent judges about a frame (its
// path, its session, what it may ask of this machine, its builds, the BrowserStack key) is judged exactly as before, and the
// header is judged by none of it.
//
// WHAT A CLIENT COULD DO WITH IT. Mark its own requests, so that Appium writes less about them in its log on this computer — its
// own requests only: the header acts on the request that carries it and on no other. A request without it is logged as before.
//
// IT GOES WHERE THE FRAME GOES: to the Appium on this computer, or to BrowserStack's hub for a session on one of its phones,
// exactly as the frame carries it. A frame keeps its shape — the header is one more entry in the headers it always carried — so
// an agent one version back forwards such a frame as it did, without the header, and a farm one version back never sends it.
const appiumIsSensitive = "X-Appium-Is-Sensitive"
