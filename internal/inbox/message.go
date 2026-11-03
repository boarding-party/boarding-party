// Package inbox parses inbound persona email and routes it to a persona.
package inbox

import "time"

// Message is one received email, reduced to what a persona agent needs.
type Message struct {
	ID       string    `json:"id"`
	Persona  string    `json:"persona"`
	From     string    `json:"from"`
	To       []string  `json:"to"`
	Subject  string    `json:"subject"`
	Text     string    `json:"text"`
	Links    []string  `json:"links"`
	Received time.Time `json:"received"`
	// Filtered is why the message was not delivered to the persona; empty when delivered.
	Filtered string `json:"filtered"`
}
