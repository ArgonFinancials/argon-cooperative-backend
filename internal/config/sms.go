package config

import (
	"fmt"
	"os"
	"strings"
)

// loadSMS: which sender hands sign-in codes to phones (the Termii sender,
// piece 1, approved 2026-10-28; written against TestApiSms06). Only with an
// auth service: without one there is no sign-in to send codes for.
//
// ARGON_SMS has no default. "refusing" is said, never inherited: a forgotten
// line must not quietly route every new device to an officer. termii's three
// settings have no defaults either, and the URL must be https because the key
// travels in the request body. No refusal here repeats a value.
func (c *Config) loadSMS() error {
	if c.AuthDatabaseURL == "" {
		return nil
	}
	c.SMS = os.Getenv("ARGON_SMS")
	switch c.SMS {
	case "refusing":
		return nil
	case "termii":
	default:
		return fmt.Errorf("ARGON_SMS must be set to termii or refusing (deploy/AUTH_SMS.md)")
	}
	c.TermiiURL = os.Getenv("TERMII_URL")
	c.TermiiAPIKey = os.Getenv("TERMII_API_KEY")
	c.TermiiSenderID = os.Getenv("TERMII_SENDER_ID")
	switch {
	case !strings.HasPrefix(c.TermiiURL, "https://"):
		return fmt.Errorf("ARGON_SMS=termii: TERMII_URL must be set, and https (the key travels in the body)")
	case c.TermiiAPIKey == "":
		return fmt.Errorf("ARGON_SMS=termii: TERMII_API_KEY must be set")
	case c.TermiiSenderID == "":
		return fmt.Errorf("ARGON_SMS=termii: TERMII_SENDER_ID must be set (the registered sender ID)")
	}
	return nil
}
