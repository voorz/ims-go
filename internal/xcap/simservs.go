package xcap

import (
	"encoding/xml"
	"fmt"
	"strings"
)

const simservsNamespace = "http://uri.etsi.org/ngn/params/xml/simservs/xcap"

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

func ParseSimservs(raw []byte, etag, xui string) (Document, error) {
	var doc Document
	if err := xml.Unmarshal(raw, &doc); err != nil {
		return Document{}, fmt.Errorf("xcap: parse simservs: %w", err)
	}
	doc.Raw = append([]byte(nil), raw...)
	doc.ETag = strings.Trim(strings.TrimSpace(etag), `"`)
	doc.XUI = strings.TrimSpace(xui)
	return doc, nil
}

func (doc Document) Marshal() ([]byte, error) {
	if doc.XMLNS == "" {
		doc.XMLNS = simservsNamespace
	}
	body, err := xml.Marshal(doc)
	if err != nil {
		return nil, err
	}
	return append([]byte(xml.Header), body...), nil
}

func (doc *Document) SetOIR(active bool, restricted bool) {
	behaviour := "presentation-not-restricted"
	if restricted {
		behaviour = "presentation-restricted"
	}
	doc.OIR = &OIR{Active: active, DefaultBehaviour: behaviour}
}

func (doc *Document) SetCFU(active bool, target string) {
	if doc.CDIV == nil {
		doc.CDIV = &CDIV{}
	}
	doc.CDIV.Active = active
	doc.CDIV.Rules.XMLNS = "urn:ietf:params:xml:ns:common-policy"
	rule := Rule{ID: "cfu", Actions: Actions{ForwardTo: &ForwardTo{Target: strings.TrimSpace(target)}}}
	if active {
		uncond := struct{}{}
		rule.Conditions.Unconditional = &uncond
	}
	replaced := false
	for i, existing := range doc.CDIV.Rules.Rules {
		if existing.ID == "cfu" {
			doc.CDIV.Rules.Rules[i] = rule
			replaced = true
			break
		}
	}
	if !replaced {
		doc.CDIV.Rules.Rules = append(doc.CDIV.Rules.Rules, rule)
	}
}

func (doc *Document) SetBarring(incoming, outgoing bool) {
	doc.ICB = &Barring{Active: incoming}
	doc.OCB = &Barring{Active: outgoing}
}

func (doc Document) CFUTarget() string {
	if doc.CDIV == nil {
		return ""
	}
	for _, rule := range doc.CDIV.Rules.Rules {
		if rule.ID == "cfu" && rule.Actions.ForwardTo != nil {
			return strings.TrimSpace(rule.Actions.ForwardTo.Target)
		}
	}
	return ""
}

func (doc Document) IdentityRestricted() bool {
	return doc.OIR != nil && doc.OIR.Active && strings.Contains(doc.OIR.DefaultBehaviour, "restricted") &&
		!strings.Contains(doc.OIR.DefaultBehaviour, "not-restricted")
}
