package farm

// The refusal caps, exposed to the external seals in refusal_test.go — which hold the read and the printed reason TO
// them, so moving a cap moves what the seals demand, and a seal can never quietly disagree with the code it guards.
var (
	RefusalBodyLimit = refusalBodyLimit
	ReasonLimit      = reasonLimit
)
