package itn_orchestrator

import (
	"net/http"

	logging "github.com/ipfs/go-log/v2"
)

type RoundTripper struct {
	logger logging.StandardLogger
}

func (t RoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	body, err := readBody(req)
	if err != nil {
		return nil, err
	}
	reqBodyString := string(body)

	var headers string
	for name, values := range req.Header {
		for _, value := range values {
			headers += "\tHeaders: " + name + ": " + value + "\n"
		}
	}
	t.logger.Debugf("RoundTripper: %s %s %s\nHeaders: %s", req.Method, req.URL, reqBodyString, headers)

	resp, err := http.DefaultTransport.RoundTrip(req)
	if err != nil {
		return resp, err
	}

	t.logger.Debugf("RoundTripper: %s %s %s", resp.Status, resp.Request.Method, resp.Request.URL)

	return resp, err
}
