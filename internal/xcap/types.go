package xcap

import (
	"encoding/xml"
	"net/http"
)

type Client struct {
	HTTP   *http.Client
	Host   string
	Domain string
	// OnNet is true when the HTTP client dials the IMS/XCAP PDN. 23.003
	// 13.9.1 then uses xcap.<ims-domain> (no .pub) so operator DNS can
	// answer. The .pub name is for the public Internet.
	OnNet bool
}

type Document struct {
	XMLName xml.Name `xml:"simservs"`
	XMLNS   string   `xml:"xmlns,attr"`
	OIR     *OIR     `xml:"originating-identity-presentation-restriction"`
	CDIV    *CDIV    `xml:"communication-diversion"`
	ICB     *Barring `xml:"incoming-communication-barring"`
	OCB     *Barring `xml:"outgoing-communication-barring"`
	Raw     []byte   `xml:"-"`
	ETag    string   `xml:"-"`
	XUI     string   `xml:"-"`
}

type OIR struct {
	Active           bool   `xml:"active,attr"`
	DefaultBehaviour string `xml:"default-behaviour"`
}

type CDIV struct {
	Active       bool  `xml:"active,attr"`
	NoReplyTimer int   `xml:"NoReplyTimer"`
	Rules        Rules `xml:"ruleset"`
}

type Barring struct {
	Active bool `xml:"active,attr"`
}

type Rules struct {
	XMLName xml.Name `xml:"ruleset"`
	XMLNS   string   `xml:"xmlns,attr"`
	Rules   []Rule   `xml:"rule"`
}

type Rule struct {
	ID         string     `xml:"id,attr"`
	Conditions Conditions `xml:"conditions"`
	Actions    Actions    `xml:"actions"`
}

type Conditions struct {
	Busy          *struct{} `xml:"busy"`
	NoAnswer      *struct{} `xml:"no-answer"`
	NotReachable  *struct{} `xml:"not-reachable"`
	Unconditional *struct{} `xml:"unconditional"`
}

type Actions struct {
	ForwardTo *ForwardTo `xml:"forward-to"`
}

type ForwardTo struct {
	Target string `xml:"target"`
}

// Transport is the HTTP round-tripper that should run over the XCAP PDN.
type Transport = http.RoundTripper
