// Package inbox parses inbound persona email and routes it to a persona.
package inbox

import "time"

// Message is one received email, reduced to what a persona agent needs.
type Message struct {
	ID      string   `json:"id"`
	Persona string   `json:"persona"`
	From    string   `json:"from"`
	To      []string `json:"to"`
	// Delivered holds the envelope recipients (Delivered-To, X-Original-To); they are also in To.
	Delivered []string  `json:"delivered"`
	Subject   string    `json:"subject"`
	Text      string    `json:"text"`
	Links     []string  `json:"links"`
	Received  time.Time `json:"received"`
	// Verified is true when the receiving provider's Authentication-Results vouch for the From domain.
	Verified bool `json:"verified"`
	// AuthServ is the lower-cased authserv-id of that header, naming the system that made the verdict.
	AuthServ string `json:"authServ"`
	// Filtered is why the message was not delivered to the persona; empty when delivered.
	Filtered string `json:"filtered"`

	// calendar holds the raw .ics parts; CalendarEvents resolves them once the persona's zone is known.
	calendar [][]byte `json:"-"`
}
