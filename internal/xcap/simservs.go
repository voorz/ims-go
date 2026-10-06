package xcap

import (
	"encoding/xml"
	"fmt"
	"strings"
)

const simservsNamespace = "http://uri.etsi.org/ngn/params/xml/simservs/xcap"

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
