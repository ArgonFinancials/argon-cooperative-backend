package main

// The operator's one-shot (deploy/48-society-vfd-account.sh): VFD's name
// enquiry on one account in VFD's own bank, printed as ONE JSON line on
// stdout. A society's VFD account is "recorded by the operator from VFD's
// answer, never typed" (LEDGER_CONTRACT §12): the script passes only the
// number, and records what this prints.
//
// Never printed: the BVN the enquiry carries. Nothing is stored here.
//
//	argon-api vfd-account-lookup 1000000001
//	exit 0  VFD found it       {"outcome":"accepted","code":"00","accountNo":...,"name":...,"clientId":...,"accountId":...}
//	exit 3  VFD says no such account (the outcome and VFD's code, nothing else)
//	exit 4  VFD not reached, or its answer unreadable
//	exit 2  misuse: not ten digits, or VFD_BASE_URL (https) / VFD_ACCESS_TOKEN unset

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/argon/backend/internal/vfd"
)

var tenDigits = regexp.MustCompile(`^[0-9]{10}$`)

type vfdAnswer struct {
	Outcome   string `json:"outcome"`
	Code      string `json:"code"`
	AccountNo string `json:"accountNo,omitempty"`
	Name      string `json:"name,omitempty"`
	ClientID  string `json:"clientId,omitempty"`
	AccountID string `json:"accountId,omitempty"`
}

func runVFDLookup(args []string) int {
	if len(args) != 1 || !tenDigits.MatchString(args[0]) {
		fmt.Fprintln(os.Stderr, "vfd-account-lookup: one account number, ten digits")
		return 2
	}
	base, token := os.Getenv("VFD_BASE_URL"), os.Getenv("VFD_ACCESS_TOKEN")
	var key []byte
	if k := os.Getenv("VFD_PROXY_KEY"); k != "" {
		b, err := hex.DecodeString(k)
		if err != nil || len(b) < 32 {
			fmt.Fprintln(os.Stderr, "vfd-account-lookup: VFD_PROXY_KEY must be at least 32 bytes as hex")
			return 2
		}
		key = b
	}
	if !strings.HasPrefix(base, "https://") || (token == "") == (key == nil) {
		fmt.Fprintln(os.Stderr, "vfd-account-lookup: VFD_BASE_URL (https) and exactly one of VFD_PROXY_KEY (through the relay) or VFD_ACCESS_TOKEN must be set (deploy/README.md, the wallet)")
		return 2
	}
	// The client logs the last four digits and the outcome, to stderr: stdout
	// is the answer and nothing else.
	client, err := vfd.New(vfd.Config{BaseURL: base, AccessToken: token, ProxyKey: key, Timeout: 15 * time.Second,
		Log: slog.New(slog.NewTextHandler(os.Stderr, nil))})
	if err != nil {
		fmt.Fprintln(os.Stderr, "vfd-account-lookup:", err)
		return 2
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	l, err := client.NameEnquiry(ctx, args[0], vfd.IntraBank)
	if err != nil {
		fmt.Fprintln(os.Stderr, "vfd-account-lookup:", err)
		return 4
	}
	a := vfdAnswer{Outcome: string(l.Outcome), Code: l.Code}
	if l.Outcome == vfd.Accepted {
		a.AccountNo, a.Name, a.ClientID, a.AccountID = l.Party.AccountNo, l.Party.Name, l.Party.ClientID, l.Party.AccountID
	}
	b, _ := json.Marshal(a)
	fmt.Println(string(b))
	switch l.Outcome {
	case vfd.Accepted:
		return 0
	case vfd.Refused:
		return 3
	}
	return 4
}
