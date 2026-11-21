package main

import (
	"bufio"
	"crypto/rand"
	"fmt"
	"math/big"
	"os"
	"strings"

	"github.com/argon/backend/internal/auth"
)

// runDemoPIN: argon-api demo-pin [PHONE], a PIN on stdin or an empty line.
// The phone, when given, is held to the rule too (a PIN is never the end of
// the person's own number).
func runDemoPIN(args []string) int {
	var phones []string
	if len(args) == 1 {
		phones = append(phones, args[0])
	}
	line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	pin := strings.TrimSpace(line)
	if pin == "" {
		for {
			n, err := rand.Int(rand.Reader, big.NewInt(1_000_000))
			if err != nil {
				fmt.Fprintln(os.Stderr, "demo-pin: no randomness:", err)
				return 2
			}
			if pin = fmt.Sprintf("%06d", n.Int64()); auth.CheckNewPIN(pin, phones...) == nil {
				break
			}
		}
	} else if err := auth.CheckNewPIN(pin, phones...); err != nil {
		fmt.Fprintln(os.Stderr, "demo-pin: that PIN is too easy to guess (six digits, not a run, not a repeat, not the end of the phone number)")
		return 3
	}
	h, err := auth.HashPINWith(auth.ServerArgon, pin)
	if err != nil {
		fmt.Fprintln(os.Stderr, "demo-pin:", err)
		return 2
	}
	fmt.Println(pin, h)
	return 0
}
