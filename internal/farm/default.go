package farm

// DefaultBaseURL is the farm connect connects to when neither --farm nor NTH_AGENT_FARM names one (WHAT_THE_FIRST_TRY_SHOWED).
// A customer has one farm to connect to. It is an https address, so it passes ValidateBaseURL like any other, and the
// flag and the variable still win, with every rule they have.
const DefaultBaseURL = "https://farm.nthcase.com"
