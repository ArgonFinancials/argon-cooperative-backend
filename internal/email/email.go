// Package email sends the enrolment codes officers receive by email
// (0034; LEDGER_CONTRACT §12, 2026-11-20). One message, one recipient, plain
// text. Nothing here logs a message's body: it carries a code.
package email

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sesv2"
	"github.com/aws/aws-sdk-go-v2/service/sesv2/types"
)

// Message: one plain-text email to one address.
type Message struct {
	To      string
	Subject string
	Text    string
}

// Sender sends a message and answers the provider's id for it, or why not.
type Sender interface {
	Send(ctx context.Context, m Message) (providerID string, err error)
}

// ErrNotConfigured: this server has no sender (ARGON_EMAIL=off).
var ErrNotConfigured = errors.New("email: no sender is configured on this server")

var oneAddress = regexp.MustCompile(`^[^@\s,;<>]+@[^@\s,;<>]+\.[^@\s,;<>.]+$`)

// SES sends through Amazon SES (v2 API) with the instance's own role: no key
// is stored anywhere. From is the verified sender address.
type SES struct {
	client *sesv2.Client
	from   string
	log    *slog.Logger
}

// NewSES loads the AWS credentials the default way (on the instance, its
// role through the metadata service) for region.
func NewSES(ctx context.Context, region, from string, log *slog.Logger) (*SES, error) {
	if !oneAddress.MatchString(from) {
		return nil, fmt.Errorf("email: EMAIL_FROM must be one address")
	}
	cfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(region))
	if err != nil {
		return nil, fmt.Errorf("email: AWS configuration: %w", err)
	}
	return &SES{client: sesv2.NewFromConfig(cfg), from: from, log: log}, nil
}

func (s *SES) Send(ctx context.Context, m Message) (string, error) {
	if !oneAddress.MatchString(m.To) {
		return "", fmt.Errorf("email: not one address")
	}
	out, err := s.client.SendEmail(ctx, &sesv2.SendEmailInput{
		FromEmailAddress: aws.String(s.from),
		Destination:      &types.Destination{ToAddresses: []string{m.To}},
		Content: &types.EmailContent{Simple: &types.Message{
			Subject: &types.Content{Data: aws.String(m.Subject), Charset: aws.String("UTF-8")},
			Body:    &types.Body{Text: &types.Content{Data: aws.String(m.Text), Charset: aws.String("UTF-8")}},
		}},
	})
	domain := m.To[strings.LastIndex(m.To, "@")+1:]
	if err != nil {
		s.log.ErrorContext(ctx, "email not sent", "to_domain", domain, "err", err.Error())
		return "", fmt.Errorf("email: SES: %w", err)
	}
	id := aws.ToString(out.MessageId)
	s.log.InfoContext(ctx, "email sent", "to_domain", domain, "provider_id", id)
	return id, nil
}

// Masked: the address as an officer may see it once a code is sent: the first
// letter, then •••, then @domain. "c•••@argon-financials.com".
func Masked(addr string) string {
	at := strings.LastIndex(addr, "@")
	if at < 1 {
		return "•••"
	}
	r := []rune(addr[:at])
	return string(r[0]) + "•••" + addr[at:]
}
