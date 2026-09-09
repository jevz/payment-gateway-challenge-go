package payment

import "encoding/json"

// PAN is a full card number. Formatting or JSON-encoding it produces the
// masked form; the raw value is only reachable through an explicit string
// conversion, which the service does once when building the bank request.
//
// The methods have value receivers so the masking also applies when a PAN is
// held by value inside another struct.
type PAN string

// LastFour returns the last four digits.
func (p PAN) LastFour() string {
	if len(p) < 4 {
		return ""
	}
	return string(p[len(p)-4:])
}

// String returns the masked card number.
func (p PAN) String() string { return "**** **** **** " + p.LastFour() }

// MarshalJSON encodes the masked card number.
func (p PAN) MarshalJSON() ([]byte, error) {
	return json.Marshal(p.String())
}
