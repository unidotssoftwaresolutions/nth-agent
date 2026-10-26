package farmtest

import (
	"fmt"
	"net/http"
)

// GatewayPage is the page nginx answers with when the farm behind it is not there (NTH_AGENT_0_4_4 item 1): a 502 while
// the farm's API restarts, a 504 when it does not answer in time, a 503. nginx's own words, as its default error page
// writes them: no envelope, no sentence of the farm's.
func GatewayPage(status int) string {
	title := fmt.Sprintf("%d %s", status, http.StatusText(status))
	switch status {
	case http.StatusServiceUnavailable:
		title = "503 Service Temporarily Unavailable"
	case http.StatusGatewayTimeout:
		title = "504 Gateway Time-out"
	}
	return "<html>\r\n<head><title>" + title + "</title></head>\r\n<body>\r\n<center><h1>" + title +
		"</h1></center>\r\n<hr><center>nginx</center>\r\n</body>\r\n</html>\r\n"
}

// EnvelopeSayingNothing is the farm's error envelope with no sentence in it: the farm's own answer, with no reason.
const EnvelopeSayingNothing = `{"success":false,"data":null,"message":null,"errors":null}`
